package stackcmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/jamestiberiuskirk/stackr/internal/config"
)

// StackType indicates whether a stack is local or remote
type StackType string

const (
	// StackTypeLocal indicates a stack with local docker-compose.yml
	StackTypeLocal StackType = "local"
	// StackTypeRemote indicates a stack defined by remote Git repository
	StackTypeRemote StackType = "remote"
)

// StackInfo contains information about a discovered stack
type StackInfo struct {
	Name         string    // Stack name
	Type         StackType // Local or remote
	ComposePaths []string  // Full paths to compose files (first is primary)
}

// PrimaryComposePath returns the first (primary) compose file path.
func (s StackInfo) PrimaryComposePath() string {
	if len(s.ComposePaths) == 0 {
		return ""
	}
	return s.ComposePaths[0]
}

// DiscoverStacks scans the stacks directory and returns both local and remote stacks
func DiscoverStacks(cfg config.Config) ([]StackInfo, error) {
	entries, err := os.ReadDir(cfg.StacksDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read stacks directory: %w", err)
	}

	var stacks []StackInfo

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		stackName := entry.Name()
		stackDir := filepath.Join(cfg.StacksDir, stackName)

		info, err := resolveStack(cfg, stackName, stackDir)
		if err != nil {
			return nil, err
		}
		if info != nil {
			stacks = append(stacks, *info)
		}
	}

	// Sort by name for consistency
	sort.Slice(stacks, func(i, j int) bool {
		return stacks[i].Name < stacks[j].Name
	})

	return stacks, nil
}

// ResolveStackPath resolves a stack name to its compose paths and type
func ResolveStackPath(cfg config.Config, stackName string) (StackInfo, error) {
	stackDir := filepath.Join(cfg.StacksDir, stackName)

	// Check if stack directory exists
	if !dirExists(stackDir) {
		return StackInfo{}, fmt.Errorf("stack %q does not exist", stackName)
	}

	info, err := resolveStack(cfg, stackName, stackDir)
	if err != nil {
		return StackInfo{}, err
	}
	if info == nil {
		return StackInfo{}, fmt.Errorf("stack %q has neither docker-compose.yml, stackr/config.yaml, nor stackr-repo.yml", stackName)
	}
	return *info, nil
}

// resolveStack loads StackLocalConfig and builds a StackInfo. Returns nil if the
// directory does not look like a stack (no compose file, no config).
func resolveStack(cfg config.Config, stackName, stackDir string) (*StackInfo, error) {
	localCfg, err := config.LoadStackLocalConfig(stackDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load config for stack %q: %w", stackName, err)
	}

	if localCfg.IsRemote() {
		return resolveRemoteStack(cfg, stackName, localCfg)
	}

	return resolveLocalStack(stackDir, stackName, localCfg)
}

func resolveLocalStack(stackDir, stackName string, localCfg *config.StackLocalConfig) (*StackInfo, error) {
	// Build compose paths relative to the stack dir
	var paths []string
	for _, f := range localCfg.ComposeFiles {
		paths = append(paths, filepath.Join(stackDir, f))
	}

	// At least the primary compose file must exist
	if len(paths) == 0 || !fileExists(paths[0]) {
		return nil, nil
	}

	return &StackInfo{
		Name:         stackName,
		Type:         StackTypeLocal,
		ComposePaths: paths,
	}, nil
}

func resolveRemoteStack(cfg config.Config, stackName string, localCfg *config.StackLocalConfig) (*StackInfo, error) {
	remoteRepoDir := cfg.Global.RemoteStacksDir
	if !filepath.IsAbs(remoteRepoDir) {
		remoteRepoDir = filepath.Join(cfg.RepoRoot, remoteRepoDir)
	}

	baseDir := filepath.Join(remoteRepoDir, stackName)
	if localCfg.RemoteRepo.Path != "" && localCfg.RemoteRepo.Path != "." {
		baseDir = filepath.Join(remoteRepoDir, stackName, localCfg.RemoteRepo.Path)
	}

	var paths []string
	for _, f := range localCfg.ComposeFiles {
		paths = append(paths, filepath.Join(baseDir, f))
	}

	return &StackInfo{
		Name:         stackName,
		Type:         StackTypeRemote,
		ComposePaths: paths,
	}, nil
}

// fileExists checks if a file exists
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// dirExists checks if a directory exists
func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false
		}
		// Other errors (permission, etc.) - assume doesn't exist
		return false
	}
	return info.IsDir()
}
