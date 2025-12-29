package stackcmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/jamestiberiuskirk/stackr/internal/config"
	"github.com/jamestiberiuskirk/stackr/internal/envfile"
)

var (
	envVarPattern = regexp.MustCompile(`(?m)(^|[^$])\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
	closingMarker = "##########################"
)

type Options struct {
	Debug       bool
	DryRun      bool
	All         bool
	TearDown    bool
	Update      bool
	Backup      bool
	VarsOnly    bool
	GetVars     bool
	Compose     bool
	Init        bool
	Stacks      []string
	VarsCommand []string
	Tag         string
}

type Manager struct {
	cfg        config.Config
	envFile    string
	envValues  map[string]string
	envContent string
	targetDir  string
	backupDir  string
	baseEnv    map[string]string
	poolBases  map[string]string
	stdout     io.Writer
	stderr     io.Writer
}

func NewManager(cfg config.Config) (*Manager, error) {
	return NewManagerWithWriters(cfg, os.Stdout, os.Stderr)
}

func NewManagerWithWriters(cfg config.Config, stdout, stderr io.Writer) (*Manager, error) {
	log.Printf("NewManager: EnvFile=%s RepoRoot=%s HostRepoRoot=%s", cfg.EnvFile, cfg.RepoRoot, cfg.HostRepoRoot)
	envValues, envContent, err := readEnvFile(cfg.EnvFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read env file %s: %w", cfg.EnvFile, err)
	}

	targetDir := cfg.StacksDir
	envValues["COMPOSE_DIRECTORY"] = targetDir

	// Backup dir is for internal stackr use only, not injected as env var
	backupDir := absolutePath(cfg.RepoRoot, cfg.Global.Paths.BackupDir)

	for key, value := range cfg.Global.Env.Global {
		envValues[key] = value
	}

	poolBases := make(map[string]string)
	for name, rel := range cfg.Global.Paths.Pools {
		key := strings.ToUpper(strings.TrimSpace(name))
		if key == "" {
			return nil, fmt.Errorf("paths.pools contains empty key")
		}
		p := absolutePath(cfg.RepoRoot, rel)
		poolBases[key] = p
	}

	baseEnv := envMapFromOS()
	for k, v := range envValues {
		baseEnv[k] = v
		_ = os.Setenv(k, v)
	}

	return &Manager{
		cfg:       cfg,
		envFile:   cfg.EnvFile,
		envValues: envValues,
		envContent: envContent,
		targetDir: targetDir,
		backupDir: backupDir,
		baseEnv:   baseEnv,
		poolBases: poolBases,
		stdout:    stdout,
		stderr:    stderr,
	}, nil
}

func (m *Manager) Run(ctx context.Context, opts Options) error {
	if opts.VarsOnly && len(opts.VarsCommand) == 0 && !opts.Compose {
		return errors.New("vars-only requires a command after --")
	}
	if opts.Compose && len(opts.VarsCommand) == 0 {
		return errors.New("compose requires arguments (e.g. 'up -d', 'logs', 'ps')")
	}

	stacks := opts.Stacks
	if opts.All {
		names, err := m.loadAllStacks()
		if err != nil {
			return err
		}
		stacks = names
	}

	stacks = dedupePreserve(stacks)
	if len(stacks) == 0 {
		return errors.New("no stacks specified")
	}

	if opts.Debug {
		debugf(true, "env file: %s", m.envFile)
		debugf(true, "dry run: %v", opts.DryRun)
		debugf(true, "stacks dir: %s", m.targetDir)
		debugf(true, "service list: %s", strings.Join(stacks, ", "))
		debugf(true, "all services: %v", opts.All)
		debugf(true, "tear down: %v", opts.TearDown)
		debugf(true, "update: %v", opts.Update)
		debugf(true, "backup: %v", opts.Backup)
		debugf(true, "vars only: %v", opts.VarsOnly)
		debugf(true, "get vars: %v", opts.GetVars)
	}

	if opts.Backup && m.backupDir == "" {
		return errors.New("BACKUP_DIR is not set in .env")
	}

	for _, stack := range stacks {
		fmt.Printf("Stack: %s\n", stack)
		if err := m.runStack(ctx, stack, opts); err != nil {
			return err
		}
	}

	return nil
}

func (m *Manager) runStack(ctx context.Context, stack string, opts Options) error {
	stackDir := filepath.Join(m.targetDir, stack)
	composePath := filepath.Join(stackDir, "docker-compose.yml")
	if _, err := os.Stat(composePath); err != nil {
		return fmt.Errorf("stack %s: %w", stack, err)
	}

	// Update .env with new tag if specified
	if opts.Tag != "" && opts.Update {
		tagEnv := strings.ToUpper(stack) + "_IMAGE_TAG"
		previous, err := envfile.Update(m.cfg.EnvFile, tagEnv, opts.Tag)
		if err != nil {
			return fmt.Errorf("failed to update %s: %w", tagEnv, err)
		}
		log.Printf("updated %s to %s (previous: %s)", tagEnv, opts.Tag, previous)

		// Reload env values after update
		envValues, envContent, err := readEnvFile(m.cfg.EnvFile)
		if err != nil {
			return fmt.Errorf("failed to reload env file: %w", err)
		}
		m.envValues = envValues
		m.envContent = envContent
	}

	if opts.Backup {
		debugf(opts.Debug, "%s: starting backup", stack)
		return m.backupStack(stack, stackDir, opts)
	}

	vars, err := collectEnvVars(composePath)
	if err != nil {
		return fmt.Errorf("stack %s: failed to parse env vars: %w", stack, err)
	}

	if opts.GetVars {
		debugf(opts.Debug, "%s: collecting missing vars", stack)
		return m.ensureStackVars(stack, vars, opts)
	}

	debugf(opts.Debug, "%s: running compose operations", stack)
	return m.runCompose(ctx, stack, composePath, vars, opts)
}

func (m *Manager) loadAllStacks() ([]string, error) {
	entries, err := os.ReadDir(m.targetDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read stacks dir %s: %w", m.targetDir, err)
	}

	var stacks []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		stack := entry.Name()
		composePath := filepath.Join(m.targetDir, stack, "docker-compose.yml")
		if _, err := os.Stat(composePath); err == nil {
			stacks = append(stacks, stack)
		}
	}

	sort.Strings(stacks)
	return stacks, nil
}

func (m *Manager) backupStack(stack, stackDir string, opts Options) error {
	if m.backupDir == "" {
		return errors.New("BACKUP_DIR is not set")
	}

	timestamp := time.Now().Format("20060102_150405")
	dest := filepath.Join(m.backupDir, timestamp, stack)

	if opts.DryRun {
		fmt.Printf("[DRY RUN] Would create backup at: %s\n", dest)
	} else {
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return fmt.Errorf("failed to create backup dir %s: %w", dest, err)
		}
		fmt.Printf("Creating backup at: %s\n", dest)
	}

	// Backup stack config directories
	configDirs := []struct {
		src string
		dst string
	}{
		{filepath.Join(stackDir, "config"), filepath.Join(dest, "config")},
		{filepath.Join(stackDir, "dashboards"), filepath.Join(dest, "dashboards")},
		{filepath.Join(stackDir, "dynamic"), filepath.Join(dest, "dynamic")},
	}

	for _, item := range configDirs {
		if err := m.copyBackupDir(stack, item.src, item.dst, opts); err != nil {
			return err
		}
	}

	// Backup pool volumes
	for poolName, poolBase := range m.poolBases {
		poolVolume := filepath.Join(poolBase, stack)
		poolDest := filepath.Join(dest, fmt.Sprintf("pool_%s", strings.ToLower(poolName)))
		if err := m.copyBackupDir(stack, poolVolume, poolDest, opts); err != nil {
			return err
		}
	}

	if !opts.DryRun {
		fmt.Printf("Backup completed for %s\n", stack)
	}
	return nil
}

func (m *Manager) ensureStackVars(stack string, vars []string, opts Options) error {
	missing := make([]string, 0, len(vars))
	for _, v := range vars {
		if v == "STACK_STORAGE_HDD" || v == "STACK_STORAGE_SSD" || v == "STORAGE_HDD" || v == "STORAGE_SSD" {
			continue
		}
		if _, ok := m.envValues[v]; ok {
			continue
		}
		if strings.Contains(m.envContent, v) {
			continue
		}
		missing = append(missing, fmt.Sprintf("%s=", v))
	}

	if len(missing) == 0 {
		return nil
	}

	if opts.DryRun {
		fmt.Printf("[DRY RUN] Would append vars for %s: %s\n", stack, strings.Join(missing, ", "))
		return nil
	}

	updated, changed := addVarsToEnv(m.envContent, stack, missing)
	if !changed {
		return nil
	}

	if err := writeEnvFile(m.envFile, updated); err != nil {
		return fmt.Errorf("failed to update env file: %w", err)
	}

	m.envContent = updated
	for _, entry := range missing {
		key := strings.TrimSuffix(entry, "=")
		m.envValues[key] = ""
	}

	// If this is automatic validation (not get-vars), error out after appending
	if !opts.GetVars {
		varNames := make([]string, len(missing))
		for i, entry := range missing {
			varNames[i] = strings.TrimSuffix(entry, "=")
		}
		return fmt.Errorf("missing required environment variables for stack '%s': %s\nVariables have been added to %s - please fill them in and try again",
			stack, strings.Join(varNames, ", "), m.envFile)
	}

	return nil
}

func (m *Manager) runCompose(ctx context.Context, stack, composePath string, vars []string, opts Options) error {
	envMap := m.baseEnvCopy()
	stackEnv, err := m.buildStackEnv(stack)
	if err != nil {
		return err
	}
	for k, v := range stackEnv {
		envMap[k] = v
	}

	// Set legacy STACK_STORAGE_HDD and STACK_STORAGE_SSD if pools exist
	if hddPool, ok := m.poolBases["HDD"]; ok {
		envMap["STACK_STORAGE_HDD"] = filepath.Join(hddPool, stack)
	}
	if ssdPool, ok := m.poolBases["SSD"]; ok {
		envMap["STACK_STORAGE_SSD"] = filepath.Join(ssdPool, stack)
	}

	envMap["DCFP"] = composePath

	// Automatically check and append missing env vars before validation
	if err := m.ensureStackVars(stack, vars, opts); err != nil {
		return err
	}

	if err := m.validateEnvVars(vars, envMap); err != nil {
		return err
	}

	if usesVar(vars, "STACK_STORAGE_HDD") || usesVar(vars, "STORAGE_HDD") {
		if err := ensureDir(envMap["STACK_STORAGE_HDD"]); err != nil {
			return fmt.Errorf("failed to create HDD stack dir: %w", err)
		}
	}
	if usesVar(vars, "STACK_STORAGE_SSD") || usesVar(vars, "STORAGE_SSD") {
		if err := ensureDir(envMap["STACK_STORAGE_SSD"]); err != nil {
			return fmt.Errorf("failed to create SSD stack dir: %w", err)
		}
	}
	if usesVar(vars, "STACKR_PROV_POOL_SSD") {
		if err := ensureDir(envMap["STACKR_PROV_POOL_SSD"]); err != nil {
			return fmt.Errorf("failed to ensure SSD pool dir: %w", err)
		}
	}
	if usesVar(vars, "STACKR_PROV_POOL_HDD") {
		if err := ensureDir(envMap["STACKR_PROV_POOL_HDD"]); err != nil {
			return fmt.Errorf("failed to ensure HDD pool dir: %w", err)
		}
	}

	envSlice := mapToSlice(envMap)

	if opts.DryRun {
		if hdd, ok := envMap["STACK_STORAGE_HDD"]; ok {
			fmt.Println("STACK_STORAGE_HDD:", hdd)
		}
		if ssd, ok := envMap["STACK_STORAGE_SSD"]; ok {
			fmt.Println("STACK_STORAGE_SSD:", ssd)
		}
		fmt.Println(composePath)
		debugf(opts.Debug, "%s: running docker compose config", stack)
		return m.runComposeCmd(ctx, envSlice, composePath, "config")
	}

	if opts.VarsOnly {
		varsCmd := opts.VarsCommand
		// If using 'compose' shorthand, prepend docker compose command
		if opts.Compose {
			varsCmd = append([]string{"docker", "compose", "-f", composePath}, opts.VarsCommand...)
		}
		debugf(opts.Debug, "%s: executing vars-only command %s", stack, strings.Join(varsCmd, " "))
		cmd := exec.CommandContext(ctx, varsCmd[0], varsCmd[1:]...)
		cmd.Dir = m.cfg.RepoRoot
		cmd.Env = envSlice
		cmd.Stdout = m.stdout
		cmd.Stderr = m.stderr
		return cmd.Run()
	}

	if opts.TearDown {
		debugf(opts.Debug, "%s: tearing stack down", stack)
		return m.runComposeCmd(ctx, envSlice, composePath, "down")
	}

	running, err := m.composeOutput(ctx, envSlice, composePath, "ps", "-a", "--services", "--filter", "status=running")
	if err != nil {
		return err
	}
	services, err := m.composeOutput(ctx, envSlice, composePath, "ps", "-a", "--services")
	if err != nil {
		return err
	}

	if running != "" && running == services {
		debugf(opts.Debug, "%s: restarting stack (all services running)", stack)
		if err := m.runComposeCmd(ctx, envSlice, composePath, "down"); err != nil {
			return err
		}
	}

	if opts.Update {
		debugf(opts.Debug, "%s: pulling latest images", stack)
		if err := m.runComposeCmd(ctx, envSlice, composePath, "pull"); err != nil {
			return err
		}
	}

	debugf(opts.Debug, "%s: bringing stack up", stack)
	return m.runComposeCmd(ctx, envSlice, composePath, "up", "-d")
}

func (m *Manager) runComposeCmd(ctx context.Context, env []string, composePath string, args ...string) error {
	fullArgs := append([]string{"compose", "-f", composePath}, args...)
	cmd := exec.CommandContext(ctx, "docker", fullArgs...)
	cmd.Dir = m.cfg.RepoRoot
	cmd.Env = env
	cmd.Stdout = m.stdout
	cmd.Stderr = m.stderr
	return cmd.Run()
}

func (m *Manager) composeOutput(ctx context.Context, env []string, composePath string, args ...string) (string, error) {
	fullArgs := append([]string{"compose", "-f", composePath}, args...)
	cmd := exec.CommandContext(ctx, "docker", fullArgs...)
	cmd.Dir = m.cfg.RepoRoot
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker %s failed: %v\n%s", strings.Join(fullArgs, " "), err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

func (m *Manager) validateEnvVars(vars []string, env map[string]string) error {
	var missing []string
	for _, v := range vars {
		if isStorageVar(v) {
			continue
		}
		if value, ok := env[v]; !ok || strings.TrimSpace(value) == "" {
			missing = append(missing, v)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("environment variable(s) not set: %s", strings.Join(missing, ", "))
	}
	return nil
}

func collectEnvVars(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return uniqueEnvVars(string(data)), nil
}

func addVarsToEnv(content, stack string, vars []string) (string, bool) {
	if len(vars) == 0 {
		return content, false
	}

	marker := fmt.Sprintf("###### %s vars #####", strings.ToLower(stack))
	if !strings.Contains(content, marker) {
		var builder strings.Builder
		trimmed := strings.TrimRight(content, "\n")
		if trimmed != "" {
			builder.WriteString(trimmed)
			builder.WriteString("\n\n")
		}
		builder.WriteString(marker)
		builder.WriteString("\n")
		for _, line := range vars {
			builder.WriteString(line)
			builder.WriteString("\n")
		}
		builder.WriteString(closingMarker)
		builder.WriteString("\n")
		return builder.String(), true
	}

	start := strings.Index(content, marker)
	if start == -1 {
		return content, false
	}

	sectionStart := start + len(marker)
	remainder := content[sectionStart:]
	end := strings.Index(remainder, closingMarker)
	if end == -1 {
		return content, false
	}

	sectionBody := remainder[:end]
	existing := extractKeys(sectionBody)
	toInsert := make([]string, 0, len(vars))
	for _, entry := range vars {
		key := strings.TrimSuffix(entry, "=")
		if !existing[key] {
			toInsert = append(toInsert, entry)
			existing[key] = true
		}
	}
	if len(toInsert) == 0 {
		return content, false
	}

	var builder strings.Builder
	builder.WriteString(content[:sectionStart])
	if !strings.HasSuffix(content[:sectionStart], "\n") {
		builder.WriteString("\n")
	}
	builder.WriteString(strings.TrimLeft(sectionBody, "\n"))
	if !strings.HasSuffix(builder.String(), "\n") {
		builder.WriteString("\n")
	}
	for _, line := range toInsert {
		builder.WriteString(line)
		builder.WriteString("\n")
	}
	builder.WriteString(remainder[end:])
	return builder.String(), true
}

func extractKeys(section string) map[string]bool {
	keys := make(map[string]bool)
	scanner := strings.Split(section, "\n")
	for _, line := range scanner {
		trim := strings.TrimSpace(line)
		if trim == "" || trim == closingMarker {
			continue
		}
		if idx := strings.Index(trim, "="); idx > 0 {
			key := strings.TrimSpace(trim[:idx])
			if key != "" {
				keys[key] = true
			}
		}
	}
	return keys
}

func readEnvFile(path string) (map[string]string, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]string{}, "", nil
		}
		return nil, "", err
	}

	values, err := godotenv.Unmarshal(string(data))
	if err != nil {
		return nil, "", err
	}
	return values, string(data), nil
}

func writeEnvFile(path, content string) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode()
	}
	return os.WriteFile(path, []byte(content), mode)
}

func usesVar(vars []string, target string) bool {
	for _, v := range vars {
		if v == target {
			return true
		}
	}
	return false
}

func ensureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

func isStorageVar(name string) bool {
	switch name {
	case "STACK_STORAGE_HDD", "STACK_STORAGE_SSD", "STORAGE_HDD", "STORAGE_SSD":
		return true
	default:
		return false
	}
}

func uniqueEnvVars(content string) []string {
	matches := envVarPattern.FindAllStringSubmatch(content, -1)
	seen := make(map[string]struct{})
	var result []string
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		name := match[2]
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result
}

func (m *Manager) buildStackEnv(stack string) (map[string]string, error) {
	env := make(map[string]string)

	for name, base := range m.poolBases {
		path := filepath.Join(base, stack)
		env[fmt.Sprintf("STACKR_PROV_POOL_%s", name)] = path
	}

	if domain := strings.TrimSpace(m.cfg.Global.HTTP.BaseDomain); domain != "" {
		env["STACKR_PROV_DOMAIN"] = fmt.Sprintf("%s.%s", stack, domain)
	}

	if stackEnv := m.cfg.Global.Env.Stacks[stack]; len(stackEnv) > 0 {
		for k, v := range stackEnv {
			env[k] = v
		}
	}

	return env, nil
}

func dedupePreserve(values []string) []string {
	seen := make(map[string]struct{})
	var result []string
	for _, v := range values {
		if _, ok := seen[v]; ok || strings.TrimSpace(v) == "" {
			continue
		}
		seen[v] = struct{}{}
		result = append(result, v)
	}
	return result
}

func envMapFromOS() map[string]string {
	result := make(map[string]string)
	for _, kv := range os.Environ() {
		if idx := strings.Index(kv, "="); idx >= 0 {
			result[kv[:idx]] = kv[idx+1:]
		}
	}
	return result
}

func (m *Manager) baseEnvCopy() map[string]string {
	out := make(map[string]string, len(m.baseEnv))
	for k, v := range m.baseEnv {
		out[k] = v
	}
	return out
}

func mapToSlice(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for k, v := range values {
		out = append(out, fmt.Sprintf("%s=%s", k, v))
	}
	return out
}

func copyDir(src, dest string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)

		info, err := d.Info()
		if err != nil {
			return err
		}

		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode())
		case d.Type()&os.ModeSymlink != 0:
			ref, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(ref, target)
		default:
			return copyFile(path, target, info.Mode())
		}
	})
}

func copyFile(src, dest string, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		_ = in.Close()
	}()

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
	}()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}

func debugf(enabled bool, format string, args ...interface{}) {
	if !enabled {
		return
	}
	fmt.Printf("[DEBUG]: "+format+"\n", args...)
}
func (m *Manager) copyBackupDir(stack, src, dest string, opts Options) error {
	info, err := os.Stat(src)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("failed to inspect %s: %w", src, err)
	}

	if !info.IsDir() {
		return nil
	}

	if opts.DryRun {
		fmt.Printf("  [DRY RUN] Would backup: %s -> %s\n", src, dest)
		debugf(opts.Debug, "%s: skipping copy (dry run) %s", stack, src)
		return nil
	}

	if err := copyDir(src, dest); err != nil {
		return fmt.Errorf("failed to backup %s -> %s: %w", src, dest, err)
	}
	fmt.Printf("  ✓ Backed up %s\n", src)
	return nil
}

func absolutePath(root, p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return root
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(root, p)
}
