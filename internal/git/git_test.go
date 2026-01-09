package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClone(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()

	// Create a test repository to clone
	testRepo := filepath.Join(tmpDir, "source")
	require.NoError(t, os.MkdirAll(testRepo, 0o755))

	// Initialize test repo
	ctx := context.Background()
	require.NoError(t, runGitCommand(ctx, "git", "-C", testRepo, "init"))
	require.NoError(t, runGitCommand(ctx, "git", "-C", testRepo, "config", "user.email", "test@test.com"))
	require.NoError(t, runGitCommand(ctx, "git", "-C", testRepo, "config", "user.name", "Test User"))

	// Create initial commit
	testFile := filepath.Join(testRepo, "README.md")
	require.NoError(t, os.WriteFile(testFile, []byte("test"), 0o644))
	require.NoError(t, runGitCommand(ctx, "git", "-C", testRepo, "add", "."))
	require.NoError(t, runGitCommand(ctx, "git", "-C", testRepo, "commit", "-m", "initial"))

	// Test clone
	dest := filepath.Join(tmpDir, "clone")
	err := Clone(ctx, dest, CloneOptions{
		URL:    testRepo,
		Branch: "",
		Depth:  0,
	})
	require.NoError(t, err)

	// Verify clone succeeded
	clonedFile := filepath.Join(dest, "README.md")
	require.FileExists(t, clonedFile)
}

func TestCloneShallow(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()

	// Create a test repository
	testRepo := filepath.Join(tmpDir, "source")
	require.NoError(t, os.MkdirAll(testRepo, 0o755))

	ctx := context.Background()
	require.NoError(t, runGitCommand(ctx, "git", "-C", testRepo, "init"))
	require.NoError(t, runGitCommand(ctx, "git", "-C", testRepo, "config", "user.email", "test@test.com"))
	require.NoError(t, runGitCommand(ctx, "git", "-C", testRepo, "config", "user.name", "Test User"))

	// Create multiple commits
	for i := 0; i < 3; i++ {
		testFile := filepath.Join(testRepo, fmt.Sprintf("file%d.txt", i))
		require.NoError(t, os.WriteFile(testFile, []byte("test"), 0o644))
		require.NoError(t, runGitCommand(ctx, "git", "-C", testRepo, "add", "."))
		require.NoError(t, runGitCommand(ctx, "git", "-C", testRepo, "commit", "-m", fmt.Sprintf("commit %d", i)))
	}

	// Test shallow clone
	dest := filepath.Join(tmpDir, "shallow")
	err := Clone(ctx, dest, CloneOptions{
		URL:   testRepo,
		Depth: 1,
	})
	require.NoError(t, err)

	// Verify it's a shallow clone
	client := NewClient(dest)
	_, err = client.CurrentCommit(ctx)
	require.NoError(t, err)
}

func TestPull(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()

	// Create source repo
	sourceRepo := filepath.Join(tmpDir, "source")
	require.NoError(t, os.MkdirAll(sourceRepo, 0o755))

	ctx := context.Background()
	require.NoError(t, runGitCommand(ctx, "git", "-C", sourceRepo, "init"))
	require.NoError(t, runGitCommand(ctx, "git", "-C", sourceRepo, "config", "user.email", "test@test.com"))
	require.NoError(t, runGitCommand(ctx, "git", "-C", sourceRepo, "config", "user.name", "Test User"))

	// Initial commit
	testFile := filepath.Join(sourceRepo, "README.md")
	require.NoError(t, os.WriteFile(testFile, []byte("v1"), 0o644))
	require.NoError(t, runGitCommand(ctx, "git", "-C", sourceRepo, "add", "."))
	require.NoError(t, runGitCommand(ctx, "git", "-C", sourceRepo, "commit", "-m", "v1"))

	// Clone it
	cloneRepo := filepath.Join(tmpDir, "clone")
	err := Clone(ctx, cloneRepo, CloneOptions{URL: sourceRepo})
	require.NoError(t, err)

	// Add new commit to source
	require.NoError(t, os.WriteFile(testFile, []byte("v2"), 0o644))
	require.NoError(t, runGitCommand(ctx, "git", "-C", sourceRepo, "add", "."))
	require.NoError(t, runGitCommand(ctx, "git", "-C", sourceRepo, "commit", "-m", "v2"))

	// Pull in clone
	client := NewClient(cloneRepo)
	err = client.Pull(ctx)
	require.NoError(t, err)

	// Verify updated content
	content, err := os.ReadFile(filepath.Join(cloneRepo, "README.md"))
	require.NoError(t, err)
	require.Equal(t, "v2", string(content))
}

func TestCheckout(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()

	// Create test repo with tag
	testRepo := filepath.Join(tmpDir, "source")
	require.NoError(t, os.MkdirAll(testRepo, 0o755))

	ctx := context.Background()
	require.NoError(t, runGitCommand(ctx, "git", "-C", testRepo, "init"))
	require.NoError(t, runGitCommand(ctx, "git", "-C", testRepo, "config", "user.email", "test@test.com"))
	require.NoError(t, runGitCommand(ctx, "git", "-C", testRepo, "config", "user.name", "Test User"))

	// Commit v1
	testFile := filepath.Join(testRepo, "VERSION")
	require.NoError(t, os.WriteFile(testFile, []byte("v1"), 0o644))
	require.NoError(t, runGitCommand(ctx, "git", "-C", testRepo, "add", "."))
	require.NoError(t, runGitCommand(ctx, "git", "-C", testRepo, "commit", "-m", "v1"))
	require.NoError(t, runGitCommand(ctx, "git", "-C", testRepo, "tag", "v1.0.0"))

	// Commit v2
	require.NoError(t, os.WriteFile(testFile, []byte("v2"), 0o644))
	require.NoError(t, runGitCommand(ctx, "git", "-C", testRepo, "add", "."))
	require.NoError(t, runGitCommand(ctx, "git", "-C", testRepo, "commit", "-m", "v2"))

	// Clone repo (will be at v2)
	cloneRepo := filepath.Join(tmpDir, "clone")
	err := Clone(ctx, cloneRepo, CloneOptions{URL: testRepo})
	require.NoError(t, err)

	// Checkout v1 tag
	client := NewClient(cloneRepo)
	err = client.Checkout(ctx, CheckoutOptions{Ref: "v1.0.0"})
	require.NoError(t, err)

	// Verify content is v1
	content, err := os.ReadFile(filepath.Join(cloneRepo, "VERSION"))
	require.NoError(t, err)
	require.Equal(t, "v1", string(content))
}

func TestCurrentRef(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()

	// Create test repo
	testRepo := filepath.Join(tmpDir, "source")
	require.NoError(t, os.MkdirAll(testRepo, 0o755))

	ctx := context.Background()
	require.NoError(t, runGitCommand(ctx, "git", "-C", testRepo, "init"))
	require.NoError(t, runGitCommand(ctx, "git", "-C", testRepo, "config", "user.email", "test@test.com"))
	require.NoError(t, runGitCommand(ctx, "git", "-C", testRepo, "config", "user.name", "Test User"))

	// Initial commit
	testFile := filepath.Join(testRepo, "README.md")
	require.NoError(t, os.WriteFile(testFile, []byte("test"), 0o644))
	require.NoError(t, runGitCommand(ctx, "git", "-C", testRepo, "add", "."))
	require.NoError(t, runGitCommand(ctx, "git", "-C", testRepo, "commit", "-m", "initial"))

	// Get current ref
	client := NewClient(testRepo)
	ref, err := client.CurrentRef(ctx)
	require.NoError(t, err)
	require.Contains(t, []string{"master", "main"}, ref)
}

func TestIsClean(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()

	// Create test repo
	testRepo := filepath.Join(tmpDir, "source")
	require.NoError(t, os.MkdirAll(testRepo, 0o755))

	ctx := context.Background()
	require.NoError(t, runGitCommand(ctx, "git", "-C", testRepo, "init"))
	require.NoError(t, runGitCommand(ctx, "git", "-C", testRepo, "config", "user.email", "test@test.com"))
	require.NoError(t, runGitCommand(ctx, "git", "-C", testRepo, "config", "user.name", "Test User"))

	// Initial commit
	testFile := filepath.Join(testRepo, "README.md")
	require.NoError(t, os.WriteFile(testFile, []byte("test"), 0o644))
	require.NoError(t, runGitCommand(ctx, "git", "-C", testRepo, "add", "."))
	require.NoError(t, runGitCommand(ctx, "git", "-C", testRepo, "commit", "-m", "initial"))

	// Should be clean
	client := NewClient(testRepo)
	clean, err := client.IsClean(ctx)
	require.NoError(t, err)
	require.True(t, clean)

	// Make changes
	require.NoError(t, os.WriteFile(testFile, []byte("modified"), 0o644))

	// Should not be clean
	clean, err = client.IsClean(ctx)
	require.NoError(t, err)
	require.False(t, clean)
}

// runGitCommand is a helper to run git commands in tests
func runGitCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.Run()
}
