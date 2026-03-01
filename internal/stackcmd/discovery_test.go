package stackcmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jamestiberiuskirk/stackr/internal/config"
)

func TestDiscoverStacks(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	stacksDir := filepath.Join(tmpDir, "stacks")
	require.NoError(t, os.MkdirAll(stacksDir, 0o755))

	// Create local stack
	localStack := filepath.Join(stacksDir, "local-app")
	require.NoError(t, os.MkdirAll(localStack, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(localStack, "docker-compose.yml"), []byte("version: '3'"), 0o644))

	// Create remote stack
	remoteStack := filepath.Join(stacksDir, "remote-app")
	require.NoError(t, os.MkdirAll(remoteStack, 0o755))
	stackrYaml := `
remote_repo:
  url: git@github.com:org/app.git
  branch: main
  path: .
  release:
    type: tag
    ref: ${APP_VERSION}
`
	require.NoError(t, os.WriteFile(filepath.Join(remoteStack, "stackr-repo.yml"), []byte(stackrYaml), 0o644))

	// Create empty directory (should be ignored)
	emptyDir := filepath.Join(stacksDir, "empty")
	require.NoError(t, os.MkdirAll(emptyDir, 0o755))

	// Discover stacks
	cfg := config.Config{
		StacksDir: stacksDir,
		RepoRoot:  tmpDir,
		Global: config.GlobalConfig{
			RemoteStacksDir: ".stackr-repos",
		},
	}

	stacks, err := DiscoverStacks(cfg)
	require.NoError(t, err)

	// Should find 2 stacks (local and remote, not empty)
	require.Len(t, stacks, 2)

	// Verify stacks are sorted by name
	require.Equal(t, "local-app", stacks[0].Name)
	require.Equal(t, StackTypeLocal, stacks[0].Type)
	require.Contains(t, stacks[0].PrimaryComposePath(), "local-app/docker-compose.yml")

	require.Equal(t, "remote-app", stacks[1].Name)
	require.Equal(t, StackTypeRemote, stacks[1].Type)
	require.Contains(t, stacks[1].PrimaryComposePath(), ".stackr-repos/remote-app/docker-compose.yml")
}

func TestDiscoverStacks_LegacyRemoteWithLocalCompose(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	stacksDir := filepath.Join(tmpDir, "stacks")
	require.NoError(t, os.MkdirAll(stacksDir, 0o755))

	// Having both docker-compose.yml and stackr-repo.yml is valid:
	// stackr-repo.yml takes priority (remote stack)
	bothStack := filepath.Join(stacksDir, "both")
	require.NoError(t, os.MkdirAll(bothStack, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(bothStack, "docker-compose.yml"), []byte("version: '3'"), 0o644))

	stackrYaml := `
remote_repo:
  url: git@github.com:org/app.git
  release:
    type: tag
    ref: v1.0.0
`
	require.NoError(t, os.WriteFile(filepath.Join(bothStack, "stackr-repo.yml"), []byte(stackrYaml), 0o644))

	cfg := config.Config{
		StacksDir: stacksDir,
		RepoRoot:  tmpDir,
		Global: config.GlobalConfig{
			RemoteStacksDir: ".stackr-repos",
		},
	}

	stacks, err := DiscoverStacks(cfg)
	require.NoError(t, err)
	require.Len(t, stacks, 1)
	require.Equal(t, StackTypeRemote, stacks[0].Type)
}

func TestDiscoverStacks_NewConfigPath(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	stacksDir := filepath.Join(tmpDir, "stacks")

	// Create stack with stackr/config.yaml (local, multi-compose)
	stackDir := filepath.Join(stacksDir, "myapp")
	require.NoError(t, os.MkdirAll(filepath.Join(stackDir, "stackr"), 0o755))
	cfgYaml := `
compose_files:
  - docker-compose.yml
  - docker-compose.prod.yml
env:
  LOG_LEVEL: debug
`
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, "stackr", "config.yaml"), []byte(cfgYaml), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, "docker-compose.yml"), []byte("services: {}"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, "docker-compose.prod.yml"), []byte("services: {}"), 0o644))

	cfg := config.Config{
		StacksDir: stacksDir,
		RepoRoot:  tmpDir,
		Global: config.GlobalConfig{
			RemoteStacksDir: ".stackr-repos",
		},
	}

	stacks, err := DiscoverStacks(cfg)
	require.NoError(t, err)
	require.Len(t, stacks, 1)
	require.Equal(t, "myapp", stacks[0].Name)
	require.Equal(t, StackTypeLocal, stacks[0].Type)
	require.Len(t, stacks[0].ComposePaths, 2)
	require.Contains(t, stacks[0].ComposePaths[0], "docker-compose.yml")
	require.Contains(t, stacks[0].ComposePaths[1], "docker-compose.prod.yml")
}

func TestResolveStackPath_LocalStack(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	stacksDir := filepath.Join(tmpDir, "stacks")
	require.NoError(t, os.MkdirAll(stacksDir, 0o755))

	// Create local stack
	localStack := filepath.Join(stacksDir, "myapp")
	require.NoError(t, os.MkdirAll(localStack, 0o755))
	composePath := filepath.Join(localStack, "docker-compose.yml")
	require.NoError(t, os.WriteFile(composePath, []byte("version: '3'"), 0o644))

	cfg := config.Config{
		StacksDir: stacksDir,
		RepoRoot:  tmpDir,
		Global: config.GlobalConfig{
			RemoteStacksDir: ".stackr-repos",
		},
	}

	info, err := ResolveStackPath(cfg, "myapp")
	require.NoError(t, err)
	require.Equal(t, "myapp", info.Name)
	require.Equal(t, StackTypeLocal, info.Type)
	require.Equal(t, composePath, info.PrimaryComposePath())
}

func TestResolveStackPath_RemoteStack(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	stacksDir := filepath.Join(tmpDir, "stacks")
	require.NoError(t, os.MkdirAll(stacksDir, 0o755))

	// Create remote stack definition
	remoteStack := filepath.Join(stacksDir, "remote-app")
	require.NoError(t, os.MkdirAll(remoteStack, 0o755))
	stackrYaml := `
remote_repo:
  url: git@github.com:org/app.git
  branch: main
  path: deploy
  release:
    type: tag
    ref: ${APP_VERSION}
`
	require.NoError(t, os.WriteFile(filepath.Join(remoteStack, "stackr-repo.yml"), []byte(stackrYaml), 0o644))

	cfg := config.Config{
		StacksDir: stacksDir,
		RepoRoot:  tmpDir,
		Global: config.GlobalConfig{
			RemoteStacksDir: ".stackr-repos",
		},
	}

	info, err := ResolveStackPath(cfg, "remote-app")
	require.NoError(t, err)
	require.Equal(t, "remote-app", info.Name)
	require.Equal(t, StackTypeRemote, info.Type)
	require.Contains(t, info.PrimaryComposePath(), ".stackr-repos/remote-app/deploy/docker-compose.yml")
}

func TestResolveStackPath_StackNotFound(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	stacksDir := filepath.Join(tmpDir, "stacks")
	require.NoError(t, os.MkdirAll(stacksDir, 0o755))

	cfg := config.Config{
		StacksDir: stacksDir,
		RepoRoot:  tmpDir,
	}

	_, err := ResolveStackPath(cfg, "nonexistent")
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not exist")
}

func TestResolveStackPath_NoComposeOrDef(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	stacksDir := filepath.Join(tmpDir, "stacks")
	require.NoError(t, os.MkdirAll(stacksDir, 0o755))

	// Create empty stack directory
	emptyStack := filepath.Join(stacksDir, "empty")
	require.NoError(t, os.MkdirAll(emptyStack, 0o755))

	cfg := config.Config{
		StacksDir: stacksDir,
		RepoRoot:  tmpDir,
	}

	_, err := ResolveStackPath(cfg, "empty")
	require.Error(t, err)
	require.Contains(t, err.Error(), "has neither docker-compose.yml")
}

func TestResolveStackPath_LegacyWithLocalCompose(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	stacksDir := filepath.Join(tmpDir, "stacks")
	require.NoError(t, os.MkdirAll(stacksDir, 0o755))

	// Having both docker-compose.yml and stackr-repo.yml: legacy config takes priority
	bothStack := filepath.Join(stacksDir, "both")
	require.NoError(t, os.MkdirAll(bothStack, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(bothStack, "docker-compose.yml"), []byte("version: '3'"), 0o644))

	stackrYaml := `
remote_repo:
  url: git@github.com:org/app.git
  release:
    type: tag
    ref: v1.0.0
`
	require.NoError(t, os.WriteFile(filepath.Join(bothStack, "stackr-repo.yml"), []byte(stackrYaml), 0o644))

	cfg := config.Config{
		StacksDir: stacksDir,
		RepoRoot:  tmpDir,
		Global: config.GlobalConfig{
			RemoteStacksDir: ".stackr-repos",
		},
	}

	info, err := ResolveStackPath(cfg, "both")
	require.NoError(t, err)
	require.Equal(t, StackTypeRemote, info.Type)
}
