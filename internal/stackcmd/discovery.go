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
	Name        string    // Stack name
	Type        StackType // Local or remote
	ComposePath string    // Full path to docker-compose.yml
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

		// Check for docker-compose.yml (local stack)
		localComposePath := filepath.Join(stackDir, "docker-compose.yml")
		hasLocalCompose := fileExists(localComposePath)

		// Check for stackr-repo.yml (remote stack definition)
		remoteDefPath := filepath.Join(stackDir, "stackr-repo.yml")
		hasRemoteDef := fileExists(remoteDefPath)

		// Determine stack type and compose path
		if hasLocalCompose && hasRemoteDef {
			// Ambiguous: has both local compose and remote definition
			return nil, fmt.Errorf("stack %q has both docker-compose.yml and stackr-repo.yml - this is ambiguous, please use one or the other", stackName)
		}

		if hasLocalCompose {
			// Local stack
			stacks = append(stacks, StackInfo{
				Name:        stackName,
				Type:        StackTypeLocal,
				ComposePath: localComposePath,
			})
		} else if hasRemoteDef {
			// Remote stack - compose path will be in remote repo
			// Load definition to get the path
			def, err := config.LoadRemoteStackDefinition(cfg.StacksDir, stackName)
			if err != nil {
				return nil, fmt.Errorf("failed to load remote stack definition for %q: %w", stackName, err)
			}

			// Build remote compose path
			remoteRepoDir := cfg.Global.RemoteStacksDir
			if !filepath.IsAbs(remoteRepoDir) {
				remoteRepoDir = filepath.Join(cfg.RepoRoot, remoteRepoDir)
			}

			var composePath string
			if def.RemoteRepo.Path != "" && def.RemoteRepo.Path != "." {
				composePath = filepath.Join(remoteRepoDir, stackName, def.RemoteRepo.Path, "docker-compose.yml")
			} else {
				composePath = filepath.Join(remoteRepoDir, stackName, "docker-compose.yml")
			}

			stacks = append(stacks, StackInfo{
				Name:        stackName,
				Type:        StackTypeRemote,
				ComposePath: composePath,
			})
		}
		// If neither exists, skip this directory
	}

	// Sort by name for consistency
	sort.Slice(stacks, func(i, j int) bool {
		return stacks[i].Name < stacks[j].Name
	})

	return stacks, nil
}

// ResolveStackPath resolves a stack name to its compose path and type
func ResolveStackPath(cfg config.Config, stackName string) (StackInfo, error) {
	stackDir := filepath.Join(cfg.StacksDir, stackName)

	// Check if stack directory exists
	if !dirExists(stackDir) {
		return StackInfo{}, fmt.Errorf("stack %q does not exist", stackName)
	}

	// Check for local docker-compose.yml
	localComposePath := filepath.Join(stackDir, "docker-compose.yml")
	hasLocalCompose := fileExists(localComposePath)

	// Check for remote definition
	remoteDefPath := filepath.Join(stackDir, "stackr-repo.yml")
	hasRemoteDef := fileExists(remoteDefPath)

	// Validate stack type
	if hasLocalCompose && hasRemoteDef {
		return StackInfo{}, fmt.Errorf("stack %q has both docker-compose.yml and stackr-repo.yml - this is ambiguous", stackName)
	}

	if !hasLocalCompose && !hasRemoteDef {
		return StackInfo{}, fmt.Errorf("stack %q has neither docker-compose.yml nor stackr-repo.yml", stackName)
	}

	if hasLocalCompose {
		// Local stack
		return StackInfo{
			Name:        stackName,
			Type:        StackTypeLocal,
			ComposePath: localComposePath,
		}, nil
	}

	// Remote stack
	def, err := config.LoadRemoteStackDefinition(cfg.StacksDir, stackName)
	if err != nil {
		return StackInfo{}, fmt.Errorf("failed to load remote stack definition: %w", err)
	}

	// Build remote compose path
	remoteRepoDir := cfg.Global.RemoteStacksDir
	if !filepath.IsAbs(remoteRepoDir) {
		remoteRepoDir = filepath.Join(cfg.RepoRoot, remoteRepoDir)
	}

	var composePath string
	if def.RemoteRepo.Path != "" && def.RemoteRepo.Path != "." {
		composePath = filepath.Join(remoteRepoDir, stackName, def.RemoteRepo.Path, "docker-compose.yml")
	} else {
		composePath = filepath.Join(remoteRepoDir, stackName, "docker-compose.yml")
	}

	return StackInfo{
		Name:        stackName,
		Type:        StackTypeRemote,
		ComposePath: composePath,
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
