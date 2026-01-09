package stackcmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jamestiberiuskirk/stackr/internal/config"
	"github.com/jamestiberiuskirk/stackr/internal/git"
)

func TestGetRemoteStackStatus_LocalStack(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()

	// Create local stack
	stacksDir := filepath.Join(tmpDir, "stacks")
	require.NoError(t, os.MkdirAll(filepath.Join(stacksDir, "local-app"), 0o755))
	composePath := filepath.Join(stacksDir, "local-app", "docker-compose.yml")
	require.NoError(t, os.WriteFile(composePath, []byte("version: '3'"), 0o644))

	cfg := config.Config{
		RepoRoot:  tmpDir,
		StacksDir: stacksDir,
		Global: config.GlobalConfig{
			RemoteStacksDir: ".stackr-repos",
		},
	}

	status, err := GetRemoteStackStatus(cfg, "local-app")
	require.NoError(t, err)
	require.Equal(t, "local-app", status.Name)
	require.Equal(t, StackTypeLocal, status.Type)
	require.False(t, status.IsCloned)
}

func TestGetRemoteStackStatus_RemoteNotCloned(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()

	// Create remote stack definition
	stacksDir := filepath.Join(tmpDir, "stacks")
	require.NoError(t, os.MkdirAll(filepath.Join(stacksDir, "remote-app"), 0o755))

	stackrYaml := `
remote_repo:
  url: git@github.com:org/app.git
  branch: main
  release:
    type: tag
    ref: ${APP_VERSION}
`
	require.NoError(t, os.WriteFile(
		filepath.Join(stacksDir, "remote-app", "stackr-repo.yml"),
		[]byte(stackrYaml),
		0o644,
	))

	cfg := config.Config{
		RepoRoot:  tmpDir,
		StacksDir: stacksDir,
		Global: config.GlobalConfig{
			RemoteStacksDir: ".stackr-repos",
		},
	}

	status, err := GetRemoteStackStatus(cfg, "remote-app")
	require.NoError(t, err)
	require.Equal(t, "remote-app", status.Name)
	require.Equal(t, StackTypeRemote, status.Type)
	require.False(t, status.IsCloned)
	require.Equal(t, "${APP_VERSION}", status.ConfiguredRef)
}

func TestGetRemoteStackStatus_RemoteCloned(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()

	// Create source repository
	sourceRepo := filepath.Join(tmpDir, "source")
	require.NoError(t, os.MkdirAll(sourceRepo, 0o755))
	initTestGitRepo(t, sourceRepo)

	// Create remote stack definition
	stacksDir := filepath.Join(tmpDir, "stacks")
	require.NoError(t, os.MkdirAll(filepath.Join(stacksDir, "remote-app"), 0o755))

	stackrYaml := `
remote_repo:
  url: ` + sourceRepo + `
  branch: main
  release:
    type: commit
    ref: HEAD
`
	require.NoError(t, os.WriteFile(
		filepath.Join(stacksDir, "remote-app", "stackr-repo.yml"),
		[]byte(stackrYaml),
		0o644,
	))

	// Clone the repo manually
	repoPath := filepath.Join(tmpDir, ".stackr-repos", "remote-app")
	opts := git.CloneOptions{
		URL:    sourceRepo,
		Branch: "main",
		Depth:  1,
	}
	require.NoError(t, git.Clone(context.Background(), repoPath, opts))

	cfg := config.Config{
		RepoRoot:  tmpDir,
		StacksDir: stacksDir,
		Global: config.GlobalConfig{
			RemoteStacksDir: ".stackr-repos",
		},
	}

	status, err := GetRemoteStackStatus(cfg, "remote-app")
	require.NoError(t, err)
	require.Equal(t, "remote-app", status.Name)
	require.Equal(t, StackTypeRemote, status.Type)
	require.True(t, status.IsCloned)
	require.NotEmpty(t, status.CurrentVersion)
	require.False(t, status.IsDirty)
}

func TestListRemoteStacks(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()

	stacksDir := filepath.Join(tmpDir, "stacks")

	// Create local stack
	require.NoError(t, os.MkdirAll(filepath.Join(stacksDir, "local-app"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(stacksDir, "local-app", "docker-compose.yml"),
		[]byte("version: '3'"),
		0o644,
	))

	// Create remote stack
	require.NoError(t, os.MkdirAll(filepath.Join(stacksDir, "remote-app"), 0o755))
	stackrYaml := `
remote_repo:
  url: git@github.com:org/app.git
  release:
    type: tag
    ref: v1.0.0
`
	require.NoError(t, os.WriteFile(
		filepath.Join(stacksDir, "remote-app", "stackr-repo.yml"),
		[]byte(stackrYaml),
		0o644,
	))

	cfg := config.Config{
		RepoRoot:  tmpDir,
		StacksDir: stacksDir,
		Global: config.GlobalConfig{
			RemoteStacksDir: ".stackr-repos",
		},
	}

	remoteStacks, err := ListRemoteStacks(cfg)
	require.NoError(t, err)
	require.Len(t, remoteStacks, 1)
	require.Equal(t, "remote-app", remoteStacks[0].Name)
}

func TestFormatRemoteStackStatus(t *testing.T) {
	t.Helper()

	status := &RemoteStackStatus{
		Name:           "myapp",
		Type:           StackTypeRemote,
		IsCloned:       true,
		CurrentVersion: "abc123",
		ConfiguredRef:  "${APP_VERSION}",
		RepoPath:       "/path/to/repo",
		IsDirty:        false,
	}

	// Test non-verbose format
	output := FormatRemoteStackStatus(status, false)
	require.Contains(t, output, "myapp")
	require.Contains(t, output, "remote")
	require.Contains(t, output, "Cloned: true")
	require.Contains(t, output, "abc123")
	require.NotContains(t, output, "/path/to/repo")

	// Test verbose format
	verboseOutput := FormatRemoteStackStatus(status, true)
	require.Contains(t, verboseOutput, "/path/to/repo")

	// Test dirty repo warning
	status.IsDirty = true
	dirtyOutput := FormatRemoteStackStatus(status, false)
	require.Contains(t, dirtyOutput, "Warning")
	require.Contains(t, dirtyOutput, "Local changes")
}

func TestFormatRemoteStackStatus_LocalStack(t *testing.T) {
	t.Helper()

	status := &RemoteStackStatus{
		Name: "local-app",
		Type: StackTypeLocal,
	}

	output := FormatRemoteStackStatus(status, false)
	require.Contains(t, output, "local-app")
	require.Contains(t, output, "Local stack")
	require.Contains(t, output, "not managed by remote")
}

func TestFormatRemoteStackStatus_NotCloned(t *testing.T) {
	t.Helper()

	status := &RemoteStackStatus{
		Name:          "myapp",
		Type:          StackTypeRemote,
		IsCloned:      false,
		ConfiguredRef: "v1.0.0",
	}

	output := FormatRemoteStackStatus(status, false)
	require.Contains(t, output, "not yet cloned")
	require.Contains(t, output, "will clone on first deployment")
}

func TestCleanRemoteStack(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()

	// Create source repository
	sourceRepo := filepath.Join(tmpDir, "source")
	require.NoError(t, os.MkdirAll(sourceRepo, 0o755))
	initTestGitRepo(t, sourceRepo)

	// Create remote stack definition
	stacksDir := filepath.Join(tmpDir, "stacks")
	require.NoError(t, os.MkdirAll(filepath.Join(stacksDir, "remote-app"), 0o755))

	stackrYaml := `
remote_repo:
  url: ` + sourceRepo + `
  release:
    type: commit
    ref: HEAD
`
	require.NoError(t, os.WriteFile(
		filepath.Join(stacksDir, "remote-app", "stackr-repo.yml"),
		[]byte(stackrYaml),
		0o644,
	))

	// Clone the repo
	repoPath := filepath.Join(tmpDir, ".stackr-repos", "remote-app")
	opts := git.CloneOptions{
		URL:    sourceRepo,
		Branch: "main",
		Depth:  1,
	}
	require.NoError(t, git.Clone(context.Background(), repoPath, opts))

	cfg := config.Config{
		RepoRoot:  tmpDir,
		StacksDir: stacksDir,
		Global: config.GlobalConfig{
			RemoteStacksDir: ".stackr-repos",
		},
	}

	// Verify repo exists
	require.DirExists(t, repoPath)

	// Clean it
	err := CleanRemoteStack(cfg, "remote-app")
	require.NoError(t, err)

	// Verify repo is removed
	require.NoDirExists(t, repoPath)
}

func TestCleanRemoteStack_LocalStack(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()

	stacksDir := filepath.Join(tmpDir, "stacks")
	require.NoError(t, os.MkdirAll(filepath.Join(stacksDir, "local-app"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(stacksDir, "local-app", "docker-compose.yml"),
		[]byte("version: '3'"),
		0o644,
	))

	cfg := config.Config{
		RepoRoot:  tmpDir,
		StacksDir: stacksDir,
		Global: config.GlobalConfig{
			RemoteStacksDir: ".stackr-repos",
		},
	}

	// Should error when trying to clean a local stack
	err := CleanRemoteStack(cfg, "local-app")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a remote stack")
}

// Helper function to initialize a test git repo
func initTestGitRepo(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, git.RunGitCommand(context.Background(), path, "init"))
	require.NoError(t, git.RunGitCommand(context.Background(), path, "config", "user.name", "Test"))
	require.NoError(t, git.RunGitCommand(context.Background(), path, "config", "user.email", "test@example.com"))

	readme := filepath.Join(path, "README.md")
	require.NoError(t, os.WriteFile(readme, []byte("# Test"), 0o644))
	require.NoError(t, git.RunGitCommand(context.Background(), path, "add", "README.md"))
	require.NoError(t, git.RunGitCommand(context.Background(), path, "commit", "-m", "Initial commit"))
	require.NoError(t, git.RunGitCommand(context.Background(), path, "branch", "-M", "main"))
}
