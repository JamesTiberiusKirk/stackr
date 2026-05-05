package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type StackConfig struct {
	TagEnv string
	Args   []string
}

type Config struct {
	Token        string
	EnvFile      string
	Host         string
	Port         string
	RepoRoot     string
	HostRepoRoot string
	StacksDir    string
	Global       GlobalConfig
}

type GlobalConfig struct {
	Path            string        `yaml:"-"`
	Stacks          string        `yaml:"stacks_dir"`
	RemoteStacksDir string        `yaml:"remote_stacks_dir"`
	Cron            CronConfig    `yaml:"cron"`
	HTTP            HTTPConfig    `yaml:"http"`
	Paths           PathsConfig   `yaml:"paths"`
	Env             EnvConfig     `yaml:"env"`
	Auth            AuthConfig    `yaml:"auth"`
	Traefik         TraefikConfig `yaml:"traefik"`
}

// TraefikConfig captures the optional traefik integration knobs. When the
// daemon is on the same docker network as traefik, leave APIURL empty —
// the discoverer probes well-known URLs (http://traefik:8080,
// http://localhost:8081). Set APIURL only for non-default ports or
// multi-traefik setups.
type TraefikConfig struct {
	APIURL string `yaml:"api_url"`
}

// AuthConfig holds the declarative auth model. Currently only `users` is
// consumed (ADR-0002 step #1). `roles` and per-user `stacks:` overrides will
// land in step #4 (RBAC) — the YAML can already carry them; the Go struct
// will grow additively.
type AuthConfig struct {
	Users []UserConfig `yaml:"users"`
}

// UserConfig is a declared user. Fields here are public-safe — no passwords,
// no secrets. Password and TOTP state live only in SQLite and are preserved
// across boots. A user removed from this slice gets deleted from the DB on
// next sync (declarative reconciliation).
type UserConfig struct {
	Email string `yaml:"email"`
	Name  string `yaml:"name"`
	Role  string `yaml:"role"`
}

type CronConfig struct {
	DefaultProfile     string `yaml:"profile"`
	EnableFileLogs     bool   `yaml:"enable_file_logs"`
	LogsDir            string `yaml:"logs_dir"`
	ContainerRetention int    `yaml:"docker_container_retention"`
}

type HTTPConfig struct {
	BaseDomain string `yaml:"base_domain"`
}

type PathsConfig struct {
	BackupDir string            `yaml:"backup_dir"`
	Pools     map[string]string `yaml:"pools"`
	Custom    map[string]string `yaml:"custom"`
}

type EnvConfig struct {
	Global map[string]string            `yaml:"global"`
	Stacks map[string]map[string]string `yaml:"stacks"`
}

// defaultGlobalConfig is the visible (non-hidden) filename for the master
// config. Operators look at this file constantly; hiding it behind a dot
// hurts discoverability with no upside. Existing repos that still ship the
// old `.stackr.yaml` can override via the STACKR_CONFIG_FILE env var.
const defaultGlobalConfig = "stackr.yaml"

func ResolveRepoRoot(override string) (string, error) {
	override = strings.TrimSpace(override)
	if override == "" {
		return determineRepoRoot()
	}

	if !filepath.IsAbs(override) {
		abs, err := filepath.Abs(override)
		if err != nil {
			return "", fmt.Errorf("failed to resolve STACKR_REPO_ROOT: %w", err)
		}
		override = abs
	}

	info, err := os.Stat(override)
	if err != nil {
		return "", fmt.Errorf("STACKR_REPO_ROOT: %w", err)
	}

	if !info.IsDir() {
		return "", errors.New("STACKR_REPO_ROOT must point to a directory")
	}

	return override, nil
}

func Load(repoRoot string) (Config, error) {
	return loadConfig(repoRoot, true)
}

func LoadForCLI(repoRoot string) (Config, error) {
	return loadConfig(repoRoot, false)
}

func loadConfig(repoRoot string, requireToken bool) (Config, error) {
	globalCfg, globalPath, err := loadGlobalConfig(repoRoot)
	if err != nil {
		return Config{}, err
	}
	globalCfg.Path = globalPath

	token := strings.TrimSpace(os.Getenv("STACKR_TOKEN"))
	if token == "" && requireToken {
		return Config{}, errors.New("STACKR_TOKEN is required")
	}

	envFile := strings.TrimSpace(os.Getenv("STACKR_ENV_FILE"))
	if envFile == "" {
		envFile = ".env"
	}
	if !filepath.IsAbs(envFile) {
		envFile = filepath.Join(repoRoot, envFile)
	}

	host := strings.TrimSpace(os.Getenv("STACKR_HOST"))
	if host == "" {
		host = "0.0.0.0"
	}

	port := strings.TrimSpace(os.Getenv("STACKR_PORT"))
	if port == "" {
		port = "9000"
	}

	stacksDir := strings.TrimSpace(os.Getenv("STACKR_STACKS_DIR"))
	if stacksDir == "" {
		stacksDir = globalCfg.Stacks
	}
	if !filepath.IsAbs(stacksDir) {
		stacksDir = filepath.Join(repoRoot, stacksDir)
	}
	globalCfg.Stacks = stacksDir

	info, err := os.Stat(stacksDir)
	if err != nil {
		return Config{}, fmt.Errorf("failed to stat stacks dir %s: %w", stacksDir, err)
	}
	if !info.IsDir() {
		return Config{}, fmt.Errorf("%s is not a directory", stacksDir)
	}

	hostRepoRoot := strings.TrimSpace(os.Getenv("STACKR_HOST_REPO_ROOT"))
	if hostRepoRoot == "" {
		hostRepoRoot = repoRoot
	}

	return Config{
		Token:        token,
		EnvFile:      envFile,
		Host:         host,
		Port:         port,
		RepoRoot:     repoRoot,
		HostRepoRoot: hostRepoRoot,
		StacksDir:    stacksDir,
		Global:       globalCfg,
	}, nil
}

func determineRepoRoot() (string, error) {
	// Use current working directory as repo root
	return os.Getwd()
}

func loadGlobalConfig(repoRoot string) (GlobalConfig, string, error) {
	path := strings.TrimSpace(os.Getenv("STACKR_CONFIG_FILE"))
	if path == "" {
		path = defaultGlobalConfig
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(repoRoot, path)
	}

	cfg := defaultGlobal()

	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, path, nil
		}
		return GlobalConfig{}, path, fmt.Errorf("failed to read stackr config %s: %w", path, err)
	}

	if err := yaml.Unmarshal(content, &cfg); err != nil {
		return GlobalConfig{}, path, fmt.Errorf("failed to parse stackr config %s: %w", path, err)
	}

	return cfg, path, nil
}

func defaultGlobal() GlobalConfig {
	return GlobalConfig{
		Stacks:          "stacks",
		RemoteStacksDir: ".stackr-repos",
		Cron: CronConfig{
			DefaultProfile:     "cron",
			EnableFileLogs:     true,
			LogsDir:            "logs/cron",
			ContainerRetention: 5,
		},
		HTTP: HTTPConfig{
			BaseDomain: "localhost",
		},
		Paths: PathsConfig{
			BackupDir: "./backups",
			Pools:     map[string]string{},
			Custom:    map[string]string{},
		},
		Env: EnvConfig{
			Global: map[string]string{},
			Stacks: map[string]map[string]string{},
		},
	}
}
