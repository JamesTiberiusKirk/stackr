package remote

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/jamestiberiuskirk/stackr/internal/config"
	"github.com/jamestiberiuskirk/stackr/internal/git"
)

// Manager handles remote stack operations
type Manager struct {
	cfg           config.Config
	gitClientFunc func(string) *git.Client
	remoteRepoDir string
}

// NewManager creates a new remote stack manager
func NewManager(cfg config.Config) *Manager {
	remoteRepoDir := cfg.Global.RemoteStacksDir
	if !filepath.IsAbs(remoteRepoDir) {
		remoteRepoDir = filepath.Join(cfg.RepoRoot, remoteRepoDir)
	}

	return &Manager{
		cfg:           cfg,
		gitClientFunc: git.NewClient,
		remoteRepoDir: remoteRepoDir,
	}
}

// EnsureRemoteStack ensures the remote stack is cloned and at the correct version
// - Always pulls to get latest .stackr-deployment.yaml (per user requirement)
// - Only changes version if needed (based on ref resolution)
// - Gracefully degrades if git unreachable (uses cached version)
func (m *Manager) EnsureRemoteStack(ctx context.Context, stackName string, envVars map[string]string) error {
	// Load remote stack definition
	def, err := config.LoadRemoteStackDefinition(m.cfg.StacksDir, stackName)
	if err != nil {
		return fmt.Errorf("failed to load remote stack definition: %w", err)
	}

	// Resolve version ref from env vars
	resolvedRef, err := config.ResolveVersionRef(def.RemoteRepo.Release.Ref, envVars)
	if err != nil {
		// Extract the env var name from the ref pattern
		ref := def.RemoteRepo.Release.Ref
		if strings.HasPrefix(ref, "${") && strings.HasSuffix(ref, "}") {
			envVar := ref[2 : len(ref)-1]
			return NewVersionRefError(stackName, ref, envVar)
		}
		return fmt.Errorf("failed to resolve version ref: %w", err)
	}

	// Determine repo root (where we clone to)
	repoRoot := filepath.Join(m.remoteRepoDir, stackName)

	// Check if repo exists
	repoExists := m.repoExists(repoRoot)

	if !repoExists {
		// Clone the repository
		log.Printf("cloning remote stack %s from %s", stackName, def.RemoteRepo.URL)
		if err := m.cloneRepo(ctx, def.RemoteRepo.URL, def.RemoteRepo.Branch, repoRoot); err != nil {
			return NewCloneError(stackName, def.RemoteRepo.URL, err)
		}
	}

	// Create git client for this repo (always uses root, not subpath)
	client := m.gitClientFunc(repoRoot)

	// Always try to pull latest changes (for .stackr-deployment.yaml updates)
	// But be graceful if it fails (network issue, etc.)
	if err := m.pullLatest(ctx, client, stackName); err != nil {
		log.Printf("warning: failed to pull latest changes for %s: %v (using cached version)", stackName, err)
		// Continue with cached version - this is the graceful degradation
	}

	// Check if we need to checkout a different version
	if err := m.ensureCorrectVersion(ctx, client, stackName, resolvedRef, def.RemoteRepo.Release.Type); err != nil {
		return NewCheckoutError(stackName, resolvedRef, def.RemoteRepo.Release.Type, err)
	}

	log.Printf("remote stack %s ready at version %s", stackName, resolvedRef)
	return nil
}

// GetCurrentVersion returns the currently checked out version
func (m *Manager) GetCurrentVersion(ctx context.Context, stackName string) (string, error) {
	// Determine repo root
	repoRoot := filepath.Join(m.remoteRepoDir, stackName)

	if !m.repoExists(repoRoot) {
		return "", fmt.Errorf("repository not cloned yet")
	}

	client := m.gitClientFunc(repoRoot)

	// Get current commit
	commit, err := client.CurrentCommit(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get current commit: %w", err)
	}

	return commit, nil
}

// BuildMergedEnv merges global config with remote deployment config
// Priority: stack-specific > remote deployment > global
func (m *Manager) BuildMergedEnv(ctx context.Context, stackName string, baseEnv map[string]string) (map[string]string, error) {
	// Load remote stack definition to get path
	def, err := config.LoadRemoteStackDefinition(m.cfg.StacksDir, stackName)
	if err != nil {
		return baseEnv, fmt.Errorf("failed to load remote stack definition: %w", err)
	}

	repoPath := m.getRepoPath(stackName, def.RemoteRepo.Path)

	// Load deployment config from remote repo
	deployConfig, err := config.LoadDeploymentConfig(repoPath)
	if err != nil {
		return baseEnv, fmt.Errorf("failed to load deployment config: %w", err)
	}

	// Merge: base env + remote deployment config
	merged := make(map[string]string)
	for k, v := range baseEnv {
		merged[k] = v
	}
	for k, v := range deployConfig.Env {
		merged[k] = v
	}

	return merged, nil
}

// getRepoPath returns the full path to the cloned repository
func (m *Manager) getRepoPath(stackName, subPath string) string {
	// If subPath is specified and not ".", use it
	if subPath != "" && subPath != "." {
		return filepath.Join(m.remoteRepoDir, stackName, subPath)
	}
	return filepath.Join(m.remoteRepoDir, stackName)
}

// repoExists checks if the repository has been cloned
func (m *Manager) repoExists(repoPath string) bool {
	gitDir := filepath.Join(repoPath, ".git")
	info, err := os.Stat(gitDir)
	return err == nil && info.IsDir()
}

// cloneRepo clones a repository with shallow clone
func (m *Manager) cloneRepo(ctx context.Context, url, branch, destination string) error {
	// Ensure parent directory exists
	parentDir := filepath.Dir(destination)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	opts := git.CloneOptions{
		URL:    url,
		Branch: branch,
		Depth:  1, // Shallow clone
	}

	if err := git.Clone(ctx, destination, opts); err != nil {
		return fmt.Errorf("git clone failed: %w", err)
	}

	return nil
}

// pullLatest attempts to pull latest changes
func (m *Manager) pullLatest(ctx context.Context, client *git.Client, stackName string) error {
	// First fetch to get latest refs
	if err := client.Fetch(ctx); err != nil {
		return fmt.Errorf("git fetch failed: %w", err)
	}

	// Then pull
	if err := client.Pull(ctx); err != nil {
		// Check if it's a "already up to date" situation
		if gitErr, ok := err.(*git.GitError); ok {
			if gitErr.ExitCode == 0 {
				return nil
			}
		}
		return fmt.Errorf("git pull failed: %w", err)
	}

	return nil
}

// ensureCorrectVersion checks out the correct version if needed
func (m *Manager) ensureCorrectVersion(ctx context.Context, client *git.Client, stackName, ref, refType string) error {
	// Get current commit
	currentCommit, err := client.CurrentCommit(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current commit: %w", err)
	}

	// If ref is a commit hash and we're already on it, done
	if refType == "commit" && currentCommit == ref {
		log.Printf("remote stack %s already at commit %s", stackName, ref)
		return nil
	}

	// Checkout the requested ref
	log.Printf("checking out %s %s for stack %s", refType, ref, stackName)
	if err := client.Checkout(ctx, git.CheckoutOptions{Ref: ref}); err != nil {
		if gitErr, ok := err.(*git.GitError); ok {
			return fmt.Errorf("git checkout failed: ref '%s' not found in repository\n"+
				"Suggestion: Run 'git ls-remote --tags %s' to see available versions\n"+
				"Error: %s", ref, stackName, gitErr.Stderr)
		}
		return fmt.Errorf("git checkout failed: %w", err)
	}

	return nil
}
