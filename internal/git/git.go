package git

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// OperationTimeout is the maximum time allowed for a single git operation.
const OperationTimeout = 5 * time.Minute

// Client wraps git operations for a specific repository
type Client struct {
	repoPath string
}

// CloneOptions configures git clone behavior
type CloneOptions struct {
	URL    string
	Branch string
	Depth  int // Shallow clone depth (0 = full clone)
}

// CheckoutOptions configures git checkout behavior
type CheckoutOptions struct {
	Ref string // Tag, commit hash, or branch name
}

// GitError provides detailed error information from git commands
type GitError struct {
	Operation string
	Command   string
	Stdout    string
	Stderr    string
	ExitCode  int
}

func (e *GitError) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("git %s failed: %s", e.Operation, strings.TrimSpace(e.Stderr))
	}
	return fmt.Sprintf("git %s failed with exit code %d", e.Operation, e.ExitCode)
}

// NewClient creates a git client for an existing repository
func NewClient(repoPath string) *Client {
	return &Client{repoPath: repoPath}
}

// withTimeout returns a context with OperationTimeout applied.
// If the parent context already has an earlier deadline, that is preserved.
func withTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, OperationTimeout)
}

// Clone clones a git repository
func Clone(ctx context.Context, destination string, opts CloneOptions) error {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	args := []string{"clone"}

	// Add branch if specified
	if opts.Branch != "" {
		args = append(args, "--branch", opts.Branch)
	}

	// Add shallow clone if depth specified
	if opts.Depth > 0 {
		args = append(args, "--depth", fmt.Sprintf("%d", opts.Depth))
	}

	// Add URL and destination
	args = append(args, opts.URL, destination)

	cmd := exec.CommandContext(ctx, "git", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return &GitError{
			Operation: "clone",
			Command:   fmt.Sprintf("git %s", strings.Join(args, " ")),
			Stdout:    stdout.String(),
			Stderr:    stderr.String(),
			ExitCode:  cmd.ProcessState.ExitCode(),
		}
	}

	return nil
}

// Pull fetches and merges changes from remote
func (c *Client) Pull(ctx context.Context) error {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "-C", c.repoPath, "pull")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return &GitError{
			Operation: "pull",
			Command:   fmt.Sprintf("git -C %s pull", c.repoPath),
			Stdout:    stdout.String(),
			Stderr:    stderr.String(),
			ExitCode:  cmd.ProcessState.ExitCode(),
		}
	}

	return nil
}

// Fetch downloads objects and refs from remote.
// If the repo is a shallow clone, it unshallows first so that all tags
// and commits are reachable for checkout.
func (c *Client) Fetch(ctx context.Context) error {
	if c.isShallow() {
		// Non-fatal: unshallow can fail if already unshallowed between checks.
		// Continue with normal fetch regardless.
		_ = c.unshallow(ctx)
	}

	ctx, cancel := withTimeout(ctx)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "-C", c.repoPath, "fetch", "--tags")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return &GitError{
			Operation: "fetch",
			Command:   fmt.Sprintf("git -C %s fetch --tags", c.repoPath),
			Stdout:    stdout.String(),
			Stderr:    stderr.String(),
			ExitCode:  cmd.ProcessState.ExitCode(),
		}
	}

	return nil
}

// isShallow returns true if the repository is a shallow clone.
func (c *Client) isShallow() bool {
	cmd := exec.Command("git", "-C", c.repoPath, "rev-parse", "--is-shallow-repository")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

// unshallow converts a shallow clone into a full clone.
func (c *Client) unshallow(ctx context.Context) error {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "-C", c.repoPath, "fetch", "--unshallow")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return &GitError{
			Operation: "fetch --unshallow",
			Command:   fmt.Sprintf("git -C %s fetch --unshallow", c.repoPath),
			Stdout:    stdout.String(),
			Stderr:    stderr.String(),
			ExitCode:  cmd.ProcessState.ExitCode(),
		}
	}
	return nil
}

// Checkout switches to a specific ref (tag, commit, or branch)
func (c *Client) Checkout(ctx context.Context, opts CheckoutOptions) error {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "-C", c.repoPath, "checkout", opts.Ref)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return &GitError{
			Operation: "checkout",
			Command:   fmt.Sprintf("git -C %s checkout %s", c.repoPath, opts.Ref),
			Stdout:    stdout.String(),
			Stderr:    stderr.String(),
			ExitCode:  cmd.ProcessState.ExitCode(),
		}
	}

	return nil
}

// CurrentRef returns the currently checked out ref
func (c *Client) CurrentRef(ctx context.Context) (string, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "-C", c.repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", &GitError{
			Operation: "rev-parse",
			Command:   fmt.Sprintf("git -C %s rev-parse --abbrev-ref HEAD", c.repoPath),
			Stdout:    stdout.String(),
			Stderr:    stderr.String(),
			ExitCode:  cmd.ProcessState.ExitCode(),
		}
	}

	return strings.TrimSpace(stdout.String()), nil
}

// CurrentCommit returns the current commit hash
func (c *Client) CurrentCommit(ctx context.Context) (string, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "-C", c.repoPath, "rev-parse", "HEAD")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", &GitError{
			Operation: "rev-parse",
			Command:   fmt.Sprintf("git -C %s rev-parse HEAD", c.repoPath),
			Stdout:    stdout.String(),
			Stderr:    stderr.String(),
			ExitCode:  cmd.ProcessState.ExitCode(),
		}
	}

	return strings.TrimSpace(stdout.String()), nil
}

// IsClean returns true if the working directory has no uncommitted changes
func (c *Client) IsClean(ctx context.Context) (bool, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "-C", c.repoPath, "status", "--porcelain")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return false, &GitError{
			Operation: "status",
			Command:   fmt.Sprintf("git -C %s status --porcelain", c.repoPath),
			Stdout:    stdout.String(),
			Stderr:    stderr.String(),
			ExitCode:  cmd.ProcessState.ExitCode(),
		}
	}

	return stdout.Len() == 0, nil
}

// RunGitCommand runs an arbitrary git command in the specified directory
// This is primarily used for testing purposes
func RunGitCommand(ctx context.Context, repoPath string, args ...string) error {
	cmdArgs := append([]string{"-C", repoPath}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return &GitError{
			Operation: args[0],
			Command:   fmt.Sprintf("git %s", strings.Join(args, " ")),
			Stderr:    stderr.String(),
			ExitCode:  cmd.ProcessState.ExitCode(),
		}
	}

	return nil
}
