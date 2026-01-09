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
	require.Contains(t, stacks[0].ComposePath, "local-app/docker-compose.yml")

	require.Equal(t, "remote-app", stacks[1].Name)
	require.Equal(t, StackTypeRemote, stacks[1].Type)
	require.Contains(t, stacks[1].ComposePath, ".stackr-repos/remote-app/docker-compose.yml")
}

func TestDiscoverStacks_AmbiguousStack(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	stacksDir := filepath.Join(tmpDir, "stacks")
	require.NoError(t, os.MkdirAll(stacksDir, 0o755))

	// Create stack with BOTH docker-compose.yml and stackr.yaml
	ambiguousStack := filepath.Join(stacksDir, "ambiguous")
	require.NoError(t, os.MkdirAll(ambiguousStack, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ambiguousStack, "docker-compose.yml"), []byte("version: '3'"), 0o644))

	stackrYaml := `
remote_repo:
  url: git@github.com:org/app.git
  release:
    type: tag
    ref: v1.0.0
`
	require.NoError(t, os.WriteFile(filepath.Join(ambiguousStack, "stackr-repo.yml"), []byte(stackrYaml), 0o644))

	cfg := config.Config{
		StacksDir: stacksDir,
		RepoRoot:  tmpDir,
		Global: config.GlobalConfig{
			RemoteStacksDir: ".stackr-repos",
		},
	}

	_, err := DiscoverStacks(cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "ambiguous")
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
	require.Equal(t, composePath, info.ComposePath)
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
	require.Contains(t, info.ComposePath, ".stackr-repos/remote-app/deploy/docker-compose.yml")
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
	require.Contains(t, err.Error(), "neither docker-compose.yml nor stackr-repo.yml")
}

func TestResolveStackPath_Ambiguous(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	stacksDir := filepath.Join(tmpDir, "stacks")
	require.NoError(t, os.MkdirAll(stacksDir, 0o755))

	// Create stack with both compose and stackr.yaml
	ambiguousStack := filepath.Join(stacksDir, "ambiguous")
	require.NoError(t, os.MkdirAll(ambiguousStack, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ambiguousStack, "docker-compose.yml"), []byte("version: '3'"), 0o644))

	stackrYaml := `
remote_repo:
  url: git@github.com:org/app.git
  release:
    type: tag
    ref: v1.0.0
`
	require.NoError(t, os.WriteFile(filepath.Join(ambiguousStack, "stackr-repo.yml"), []byte(stackrYaml), 0o644))

	cfg := config.Config{
		StacksDir: stacksDir,
		RepoRoot:  tmpDir,
	}

	_, err := ResolveStackPath(cfg, "ambiguous")
	require.Error(t, err)
	require.Contains(t, err.Error(), "ambiguous")
}
