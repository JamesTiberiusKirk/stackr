package stackcmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jamestiberiuskirk/stackr/internal/config"
	"github.com/jamestiberiuskirk/stackr/internal/git"
	"github.com/jamestiberiuskirk/stackr/internal/remote"
)

// RemoteStackStatus contains information about a remote stack's sync status
type RemoteStackStatus struct {
	Name           string
	Type           StackType
	IsCloned       bool
	CurrentVersion string
	ConfiguredRef  string
	RepoPath       string
	IsDirty        bool
	Error          string
}

// GetRemoteStackStatus returns the status of a remote stack
func GetRemoteStackStatus(cfg config.Config, stackName string) (*RemoteStackStatus, error) {
	status := &RemoteStackStatus{
		Name: stackName,
	}

	// Resolve stack info
	stackInfo, err := ResolveStackPath(cfg, stackName)
	if err != nil {
		status.Error = err.Error()
		return status, nil
	}

	status.Type = stackInfo.Type

	// Only process remote stacks
	if stackInfo.Type != StackTypeRemote {
		return status, nil
	}

	// Load per-stack config
	stackDir := filepath.Join(cfg.StacksDir, stackName)
	localCfg, err := config.LoadStackLocalConfig(stackDir)
	if err != nil || !localCfg.IsRemote() {
		errMsg := "not a remote stack"
		if err != nil {
			errMsg = fmt.Sprintf("failed to load stack config: %v", err)
		}
		status.Error = errMsg
		return status, nil
	}

	status.ConfiguredRef = localCfg.RemoteRepo.Release.Ref

	// Check if repo is cloned
	remoteRepoDir := cfg.Global.RemoteStacksDir
	if !filepath.IsAbs(remoteRepoDir) {
		remoteRepoDir = filepath.Join(cfg.RepoRoot, remoteRepoDir)
	}

	repoPath := filepath.Join(remoteRepoDir, stackName)
	status.RepoPath = repoPath

	gitDir := filepath.Join(repoPath, ".git")
	if info, err := os.Stat(gitDir); err == nil && info.IsDir() {
		status.IsCloned = true

		// Get current version
		client := git.NewClient(repoPath)
		if commit, err := client.CurrentCommit(context.Background()); err == nil {
			status.CurrentVersion = commit[:8] // Short hash
		}

		// Check if repo is dirty
		if clean, err := client.IsClean(context.Background()); err == nil {
			status.IsDirty = !clean
		}
	}

	return status, nil
}

// ListRemoteStacks lists all remote stacks and their status
func ListRemoteStacks(cfg config.Config) ([]*RemoteStackStatus, error) {
	stacks, err := DiscoverStacks(cfg)
	if err != nil {
		return nil, err
	}

	var remoteStatuses []*RemoteStackStatus
	for _, stack := range stacks {
		if stack.Type == StackTypeRemote {
			status, err := GetRemoteStackStatus(cfg, stack.Name)
			if err != nil {
				return nil, err
			}
			remoteStatuses = append(remoteStatuses, status)
		}
	}

	return remoteStatuses, nil
}

// FormatRemoteStackStatus formats a remote stack status for display
func FormatRemoteStackStatus(status *RemoteStackStatus, verbose bool) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Stack: %s\n", status.Name)
	fmt.Fprintf(&b, "  Type: %s\n", status.Type)

	if status.Error != "" {
		fmt.Fprintf(&b, "  Error: %s\n", status.Error)
		return b.String()
	}

	if status.Type != StackTypeRemote {
		b.WriteString("  (Local stack - not managed by remote sync)\n")
		return b.String()
	}

	fmt.Fprintf(&b, "  Configured Ref: %s\n", status.ConfiguredRef)
	fmt.Fprintf(&b, "  Cloned: %v\n", status.IsCloned)

	if status.IsCloned {
		fmt.Fprintf(&b, "  Current Version: %s\n", status.CurrentVersion)
		fmt.Fprintf(&b, "  Dirty: %v\n", status.IsDirty)

		if verbose {
			fmt.Fprintf(&b, "  Repository Path: %s\n", status.RepoPath)
		}

		if status.IsDirty {
			b.WriteString("  ⚠️  Warning: Local changes detected in cloned repository\n")
		}
	} else {
		b.WriteString("  ℹ️  Repository not yet cloned (will clone on first deployment)\n")
	}

	return b.String()
}

// CleanRemoteStack removes a cloned remote stack repository
func CleanRemoteStack(cfg config.Config, stackName string) error {
	// Get stack info
	stackInfo, err := ResolveStackPath(cfg, stackName)
	if err != nil {
		return err
	}

	if stackInfo.Type != StackTypeRemote {
		return fmt.Errorf("stack %q is not a remote stack", stackName)
	}

	// Determine repo path
	remoteRepoDir := cfg.Global.RemoteStacksDir
	if !filepath.IsAbs(remoteRepoDir) {
		remoteRepoDir = filepath.Join(cfg.RepoRoot, remoteRepoDir)
	}

	repoPath := filepath.Join(remoteRepoDir, stackName)

	// Check if repo exists
	if _, err := os.Stat(repoPath); os.IsNotExist(err) {
		return fmt.Errorf("repository for stack %q is not cloned", stackName)
	}

	// Remove the directory
	if err := os.RemoveAll(repoPath); err != nil {
		return fmt.Errorf("failed to remove repository: %w", err)
	}

	return nil
}

// SyncRemoteStack manually syncs a remote stack (pull latest changes and checkout configured version)
func SyncRemoteStack(cfg config.Config, stackName string, envVars map[string]string) error {
	// Get stack info
	stackInfo, err := ResolveStackPath(cfg, stackName)
	if err != nil {
		return err
	}

	if stackInfo.Type != StackTypeRemote {
		return fmt.Errorf("stack %q is not a remote stack", stackName)
	}

	// Use remote manager to sync
	mgr := remote.NewManager(cfg)
	if err := mgr.EnsureRemoteStack(context.Background(), stackName, envVars); err != nil {
		return fmt.Errorf("failed to sync remote stack: %w", err)
	}

	return nil
}
