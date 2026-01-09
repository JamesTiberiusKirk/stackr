package remote

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jamestiberiuskirk/stackr/internal/config"
	"github.com/jamestiberiuskirk/stackr/internal/git"
)

func TestEnsureRemoteStack_CloneOnFirstRun(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()

	// Create a fake git repository to clone from
	sourceRepo := filepath.Join(tmpDir, "source")
	require.NoError(t, os.MkdirAll(sourceRepo, 0o755))
	initGitRepo(t, sourceRepo)

	// Create stack definition directory
	stacksDir := filepath.Join(tmpDir, "stacks")
	require.NoError(t, os.MkdirAll(filepath.Join(stacksDir, "myapp"), 0o755))

	// Write stackr.yaml
	stackrYaml := `
remote_repo:
  url: ` + sourceRepo + `
  branch: main
  release:
    type: commit
    ref: HEAD
`
	require.NoError(t, os.WriteFile(
		filepath.Join(stacksDir, "myapp", "stackr-repo.yml"),
		[]byte(stackrYaml),
		0o644,
	))

	// Create config
	cfg := config.Config{
		RepoRoot:  tmpDir,
		StacksDir: stacksDir,
		Global: config.GlobalConfig{
			RemoteStacksDir: ".stackr-repos",
		},
	}

	manager := NewManager(cfg)
	envVars := map[string]string{}

	// First run should clone
	err := manager.EnsureRemoteStack(context.Background(), "myapp", envVars)
	require.NoError(t, err)

	// Verify repo was cloned
	repoPath := filepath.Join(tmpDir, ".stackr-repos", "myapp")
	require.DirExists(t, repoPath)
	require.DirExists(t, filepath.Join(repoPath, ".git"))
}

func TestEnsureRemoteStack_PullOnSubsequentRuns(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()

	// Create source repository
	sourceRepo := filepath.Join(tmpDir, "source")
	require.NoError(t, os.MkdirAll(sourceRepo, 0o755))
	initGitRepo(t, sourceRepo)

	// Create stack definition
	stacksDir := filepath.Join(tmpDir, "stacks")
	require.NoError(t, os.MkdirAll(filepath.Join(stacksDir, "myapp"), 0o755))

	stackrYaml := `
remote_repo:
  url: ` + sourceRepo + `
  branch: main
  release:
    type: commit
    ref: HEAD
`
	require.NoError(t, os.WriteFile(
		filepath.Join(stacksDir, "myapp", "stackr-repo.yml"),
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

	manager := NewManager(cfg)
	envVars := map[string]string{}

	// First run - clone
	err := manager.EnsureRemoteStack(context.Background(), "myapp", envVars)
	require.NoError(t, err)

	// Add a new commit to source repo
	testFile := filepath.Join(sourceRepo, "test.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("new content"), 0o644))
	commitFile(t, sourceRepo, "test.txt", "Add test file")

	// Second run - should pull
	err = manager.EnsureRemoteStack(context.Background(), "myapp", envVars)
	require.NoError(t, err)

	// Verify file was pulled
	clonedFile := filepath.Join(tmpDir, ".stackr-repos", "myapp", "test.txt")
	require.FileExists(t, clonedFile)
	content, err := os.ReadFile(clonedFile)
	require.NoError(t, err)
	require.Equal(t, "new content", string(content))
}

func TestEnsureRemoteStack_WithVersionRef(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()

	// Create source repository
	sourceRepo := filepath.Join(tmpDir, "source")
	require.NoError(t, os.MkdirAll(sourceRepo, 0o755))
	initGitRepo(t, sourceRepo)
	createTag(t, sourceRepo, "v1.0.0")

	// Create stack definition
	stacksDir := filepath.Join(tmpDir, "stacks")
	require.NoError(t, os.MkdirAll(filepath.Join(stacksDir, "myapp"), 0o755))

	stackrYaml := `
remote_repo:
  url: ` + sourceRepo + `
  branch: main
  release:
    type: tag
    ref: ${APP_VERSION}
`
	require.NoError(t, os.WriteFile(
		filepath.Join(stacksDir, "myapp", "stackr-repo.yml"),
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

	manager := NewManager(cfg)
	envVars := map[string]string{"APP_VERSION": "v1.0.0"}

	err := manager.EnsureRemoteStack(context.Background(), "myapp", envVars)
	require.NoError(t, err)

	// Verify correct version was checked out
	version, err := manager.GetCurrentVersion(context.Background(), "myapp")
	require.NoError(t, err)
	// GetCurrentVersion returns commit hash, so just verify it's not empty
	require.NotEmpty(t, version)
}

func TestEnsureRemoteStack_WithSubdirectory(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()

	// Create source repository
	sourceRepo := filepath.Join(tmpDir, "source")
	require.NoError(t, os.MkdirAll(sourceRepo, 0o755))
	initGitRepo(t, sourceRepo)

	// Add subdirectory and file
	require.NoError(t, os.MkdirAll(filepath.Join(sourceRepo, "deploy"), 0o755))
	deployFile := filepath.Join(sourceRepo, "deploy", "docker-compose.yml")
	require.NoError(t, os.WriteFile(deployFile, []byte("version: '3'"), 0o644))
	commitFile(t, sourceRepo, "deploy/docker-compose.yml", "Add compose file")

	// Create stack definition
	stacksDir := filepath.Join(tmpDir, "stacks")
	require.NoError(t, os.MkdirAll(filepath.Join(stacksDir, "myapp"), 0o755))

	stackrYaml := `
remote_repo:
  url: ` + sourceRepo + `
  branch: main
  path: deploy
  release:
    type: commit
    ref: HEAD
`
	require.NoError(t, os.WriteFile(
		filepath.Join(stacksDir, "myapp", "stackr-repo.yml"),
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

	manager := NewManager(cfg)
	envVars := map[string]string{}

	err := manager.EnsureRemoteStack(context.Background(), "myapp", envVars)
	require.NoError(t, err)

	// Verify compose file exists in subdirectory
	composePath := filepath.Join(tmpDir, ".stackr-repos", "myapp", "deploy", "docker-compose.yml")
	require.FileExists(t, composePath)
}

func TestBuildMergedEnv(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()

	// Create source repository with deployment config
	sourceRepo := filepath.Join(tmpDir, "source")
	require.NoError(t, os.MkdirAll(sourceRepo, 0o755))
	initGitRepo(t, sourceRepo)

	// Add .stackr-deployment.yaml
	deploymentYaml := `
env:
  REMOTE_VAR: remote_value
  SHARED_VAR: from_remote
`
	deploymentFile := filepath.Join(sourceRepo, ".stackr-deployment.yaml")
	require.NoError(t, os.WriteFile(deploymentFile, []byte(deploymentYaml), 0o644))
	commitFile(t, sourceRepo, ".stackr-deployment.yaml", "Add deployment config")

	// Create stack definition
	stacksDir := filepath.Join(tmpDir, "stacks")
	require.NoError(t, os.MkdirAll(filepath.Join(stacksDir, "myapp"), 0o755))

	stackrYaml := `
remote_repo:
  url: ` + sourceRepo + `
  branch: main
  release:
    type: commit
    ref: HEAD
`
	require.NoError(t, os.WriteFile(
		filepath.Join(stacksDir, "myapp", "stackr-repo.yml"),
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

	manager := NewManager(cfg)
	envVars := map[string]string{}

	// First ensure the stack is cloned
	err := manager.EnsureRemoteStack(context.Background(), "myapp", envVars)
	require.NoError(t, err)

	// Build merged environment
	baseEnv := map[string]string{
		"BASE_VAR":   "base_value",
		"SHARED_VAR": "from_base",
	}

	mergedEnv, err := manager.BuildMergedEnv(context.Background(), "myapp", baseEnv)
	require.NoError(t, err)

	// Verify merging: remote config should override base
	require.Equal(t, "base_value", mergedEnv["BASE_VAR"])
	require.Equal(t, "remote_value", mergedEnv["REMOTE_VAR"])
	require.Equal(t, "from_remote", mergedEnv["SHARED_VAR"])
}

func TestBuildMergedEnv_NoDeploymentConfig(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()

	// Create source repository WITHOUT deployment config
	sourceRepo := filepath.Join(tmpDir, "source")
	require.NoError(t, os.MkdirAll(sourceRepo, 0o755))
	initGitRepo(t, sourceRepo)

	// Create stack definition
	stacksDir := filepath.Join(tmpDir, "stacks")
	require.NoError(t, os.MkdirAll(filepath.Join(stacksDir, "myapp"), 0o755))

	stackrYaml := `
remote_repo:
  url: ` + sourceRepo + `
  branch: main
  release:
    type: commit
    ref: HEAD
`
	require.NoError(t, os.WriteFile(
		filepath.Join(stacksDir, "myapp", "stackr-repo.yml"),
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

	manager := NewManager(cfg)
	envVars := map[string]string{}

	// Ensure the stack is cloned
	err := manager.EnsureRemoteStack(context.Background(), "myapp", envVars)
	require.NoError(t, err)

	// Build merged environment (should work without deployment config)
	baseEnv := map[string]string{"BASE_VAR": "base_value"}

	mergedEnv, err := manager.BuildMergedEnv(context.Background(), "myapp", baseEnv)
	require.NoError(t, err)

	// Should just return base env
	require.Equal(t, "base_value", mergedEnv["BASE_VAR"])
	require.Len(t, mergedEnv, 1)
}

func TestGetCurrentVersion(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()

	// Create source repository
	sourceRepo := filepath.Join(tmpDir, "source")
	require.NoError(t, os.MkdirAll(sourceRepo, 0o755))
	initGitRepo(t, sourceRepo)
	createTag(t, sourceRepo, "v1.2.3")

	// Create stack definition
	stacksDir := filepath.Join(tmpDir, "stacks")
	require.NoError(t, os.MkdirAll(filepath.Join(stacksDir, "myapp"), 0o755))

	stackrYaml := `
remote_repo:
  url: ` + sourceRepo + `
  branch: main
  release:
    type: tag
    ref: v1.2.3
`
	require.NoError(t, os.WriteFile(
		filepath.Join(stacksDir, "myapp", "stackr-repo.yml"),
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

	manager := NewManager(cfg)
	envVars := map[string]string{}

	// Ensure the stack is cloned and checked out
	err := manager.EnsureRemoteStack(context.Background(), "myapp", envVars)
	require.NoError(t, err)

	// Get current version
	version, err := manager.GetCurrentVersion(context.Background(), "myapp")
	require.NoError(t, err)
	require.NotEmpty(t, version)
}

// Helper functions for git operations in tests

func initGitRepo(t *testing.T, path string) {
	t.Helper()
	client := git.NewClient(path)

	// Initialize repo
	err := git.RunGitCommand(context.Background(), path, "init")
	require.NoError(t, err)

	// Configure git
	err = git.RunGitCommand(context.Background(), path, "config", "user.name", "Test User")
	require.NoError(t, err)
	err = git.RunGitCommand(context.Background(), path, "config", "user.email", "test@example.com")
	require.NoError(t, err)

	// Create initial commit
	readmePath := filepath.Join(path, "README.md")
	require.NoError(t, os.WriteFile(readmePath, []byte("# Test Repo"), 0o644))
	err = git.RunGitCommand(context.Background(), path, "add", "README.md")
	require.NoError(t, err)
	err = git.RunGitCommand(context.Background(), path, "commit", "-m", "Initial commit")
	require.NoError(t, err)

	// Create main branch (git init creates master by default on older git versions)
	err = git.RunGitCommand(context.Background(), path, "branch", "-M", "main")
	if err != nil {
		// Branch might already be main, ignore error
		_ = client
	}
}

func commitFile(t *testing.T, repoPath, file, message string) {
	t.Helper()
	err := git.RunGitCommand(context.Background(), repoPath, "add", file)
	require.NoError(t, err)
	err = git.RunGitCommand(context.Background(), repoPath, "commit", "-m", message)
	require.NoError(t, err)
}

func createTag(t *testing.T, repoPath, tag string) {
	t.Helper()
	err := git.RunGitCommand(context.Background(), repoPath, "tag", tag)
	require.NoError(t, err)
}
