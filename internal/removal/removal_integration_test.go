//go:build integration

package removal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jamestiberiuskirk/stackr/internal/testutil"
)

func TestTrackerDetectsRemovals(t *testing.T) {
	tracker := NewTracker()
	tracker.Initialize([]string{"stackA", "stackB", "stackC"})

	// Remove stackB
	removed := tracker.Update([]string{"stackA", "stackC"})
	sort.Strings(removed)
	require.Equal(t, []string{"stackB"}, removed)

	// No new removals
	removed = tracker.Update([]string{"stackA", "stackC"})
	require.Empty(t, removed)

	// Remove stackA
	removed = tracker.Update([]string{"stackC"})
	require.Equal(t, []string{"stackA"}, removed)

	// Add a new stack
	removed = tracker.Update([]string{"stackC", "stackD"})
	require.Empty(t, removed)

	// Remove all
	removed = tracker.Update([]string{})
	sort.Strings(removed)
	require.Equal(t, []string{"stackC", "stackD"}, removed)
}

func TestCleanupWithComposeFile(t *testing.T) {
	testutil.RequireDockerAvailable(t)

	root, stackName := testutil.SetupTestRepo(t,
		testutil.WithComposeContent(`services:
  web:
    image: nginx:alpine
    ports:
      - "0:80"
`))

	composePath := filepath.Join(root, "stacks", stackName, "docker-compose.yml")

	// Bring the stack up
	cmd := exec.Command("docker", "compose", "-f", composePath, "up", "-d")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "compose up failed: %s", string(out))

	t.Cleanup(func() {
		// Safety cleanup in case the test fails before Cleanup runs
		_ = exec.Command("docker", "compose", "-f", composePath,
			"down", "--volumes", "--remove-orphans", "--timeout", "5").Run()
	})

	// Verify something is running
	require.True(t, testutil.ContainerRunningByProject(t, stackName),
		"expected container to be running before cleanup")

	// Run cleanup
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err = Cleanup(ctx, stackName, filepath.Join(root, "stacks"))
	require.NoError(t, err)

	// Verify containers are gone
	require.False(t, testutil.ContainerRunningByProject(t, stackName),
		"expected containers to be removed after cleanup")
}

func TestCleanupWithoutComposeFile(t *testing.T) {
	testutil.RequireDockerAvailable(t)

	root, stackName := testutil.SetupTestRepo(t,
		testutil.WithComposeContent(`services:
  web:
    image: nginx:alpine
    ports:
      - "0:80"
`))

	composePath := filepath.Join(root, "stacks", stackName, "docker-compose.yml")

	// Bring the stack up
	cmd := exec.Command("docker", "compose", "-f", composePath, "up", "-d")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "compose up failed: %s", string(out))

	t.Cleanup(func() {
		// Safety cleanup via label in case the test fails
		_ = exec.Command("docker", "compose", "-f", composePath,
			"down", "--volumes", "--remove-orphans", "--timeout", "5").Run()
	})

	// Verify running
	require.True(t, testutil.ContainerRunningByProject(t, stackName))

	// Delete the compose file to simulate stack directory removal
	require.NoError(t, os.RemoveAll(filepath.Join(root, "stacks", stackName)))

	// Run cleanup -- should fall back to label-based cleanup
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err = Cleanup(ctx, stackName, filepath.Join(root, "stacks"))
	require.NoError(t, err)

	// Verify containers are gone
	require.False(t, testutil.ContainerRunningByProject(t, stackName),
		"expected containers removed by label-based cleanup")
}

func TestCleanupByProjectLabelRemovesAllResources(t *testing.T) {
	testutil.RequireDockerAvailable(t)

	root, stackName := testutil.SetupTestRepo(t,
		testutil.WithComposeContent(`services:
  web:
    image: nginx:alpine
    ports:
      - "0:80"
    volumes:
      - webdata:/data
volumes:
  webdata:
`))

	composePath := filepath.Join(root, "stacks", stackName, "docker-compose.yml")

	// Bring the stack up
	cmd := exec.Command("docker", "compose", "-f", composePath, "up", "-d")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "compose up failed: %s", string(out))

	t.Cleanup(func() {
		_ = exec.Command("docker", "compose", "-f", composePath,
			"down", "--volumes", "--remove-orphans", "--timeout", "5").Run()
	})

	// Verify container and network exist
	require.True(t, testutil.ContainerExistsByProject(t, stackName),
		"expected container to exist before cleanup")
	require.True(t, testutil.NetworkExistsByProject(t, stackName),
		"expected network to exist before cleanup")

	// Delete compose file to force label-based cleanup path
	require.NoError(t, os.RemoveAll(filepath.Join(root, "stacks", stackName)))

	// Run cleanup — should fall through to cleanupByProjectLabel
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err = Cleanup(ctx, stackName, filepath.Join(root, "stacks"))
	require.NoError(t, err)

	// Verify containers gone
	require.False(t, testutil.ContainerExistsByProject(t, stackName),
		"expected containers to be removed by label-based cleanup")

	// Verify networks gone
	require.False(t, testutil.NetworkExistsByProject(t, stackName),
		"expected networks to be removed by label-based cleanup")
}

func TestHandleRemovedStack(t *testing.T) {
	testutil.RequireDockerAvailable(t)

	root, stackName := testutil.SetupTestRepo(t,
		testutil.WithStackDirs("config"),
		testutil.WithComposeContent(`services:
  web:
    image: nginx:alpine
    ports:
      - "0:80"
`))

	stackDir := filepath.Join(root, "stacks", stackName)
	composePath := filepath.Join(stackDir, "docker-compose.yml")

	// Write config data for the stack
	testutil.WriteFile(t, filepath.Join(stackDir, "config", "app.conf"), "config data")

	// Create pool data
	ssdPool := filepath.Join(root, ".ssd_pool", stackName)
	testutil.WriteFile(t, filepath.Join(ssdPool, "data.db"), "pool data")

	// Bring the stack up
	cmd := exec.Command("docker", "compose", "-f", composePath, "up", "-d")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "compose up failed: %s", string(out))

	t.Cleanup(func() {
		_ = exec.Command("docker", "compose", "-f", composePath,
			"down", "--volumes", "--remove-orphans", "--timeout", "5").Run()
	})

	// Verify stack is running
	require.True(t, testutil.ContainerRunningByProject(t, stackName))

	// Create a Handler with real config
	cfg := testutil.BuildConfigDirect(root)
	cfg.Global.Paths.Pools = map[string]string{"SSD": ".ssd_pool"}
	handler := NewHandler(cfg, HandlerConfig{
		ContinueOnArchiveError: false,
		CleanupTimeout:         30 * time.Second,
	})

	// Initialize with the stack present, then report it removed
	handler.Initialize([]string{stackName})
	handler.CheckForRemovals([]string{})

	// Verify: archive directory created
	backupDir := filepath.Join(root, "backups", "archives")
	entries, err := os.ReadDir(backupDir)
	require.NoError(t, err)
	require.NotEmpty(t, entries, "expected archive directory to be created")

	// Find the archive for our stack
	var archivePath string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), stackName+"-") {
			archivePath = filepath.Join(backupDir, e.Name())
			break
		}
	}
	require.NotEmpty(t, archivePath, "expected archive matching stack name")

	// Verify config data was archived
	data, err := os.ReadFile(filepath.Join(archivePath, "config", "app.conf"))
	require.NoError(t, err)
	require.Equal(t, "config data", string(data))

	// Verify pool data was archived
	data, err = os.ReadFile(filepath.Join(archivePath, "pool_ssd", "data.db"))
	require.NoError(t, err)
	require.Equal(t, "pool data", string(data))

	// Verify Docker resources cleaned up
	require.False(t, testutil.ContainerExistsByProject(t, stackName),
		"expected containers to be removed after handleRemovedStack")
}

func TestArchiveBeforeCleanup(t *testing.T) {
	testutil.RequireDockerAvailable(t)

	root, stackName := testutil.SetupTestRepo(t,
		testutil.WithStackDirs("config"),
		testutil.WithComposeContent(`services:
  web:
    image: nginx:alpine
    ports:
      - "0:80"
`))

	// Write test data in config
	stackDir := filepath.Join(root, "stacks", stackName)
	testutil.WriteFile(t, filepath.Join(stackDir, "config", "app.conf"), "config data")

	// Create pool dirs with data
	ssdPool := filepath.Join(root, ".ssd_pool", stackName)
	testutil.WriteFile(t, filepath.Join(ssdPool, "data.db"), "pool data")

	composePath := filepath.Join(stackDir, "docker-compose.yml")

	// Bring the stack up
	cmd := exec.Command("docker", "compose", "-f", composePath, "up", "-d")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "compose up failed: %s", string(out))

	t.Cleanup(func() {
		_ = exec.Command("docker", "compose", "-f", composePath,
			"down", "--volumes", "--remove-orphans", "--timeout", "5").Run()
	})

	// Archive
	archiveCfg := ArchiveConfig{
		BackupDir: filepath.Join(root, "backups"),
		PoolBases: map[string]string{"SSD": filepath.Join(root, ".ssd_pool")},
		StacksDir: filepath.Join(root, "stacks"),
	}

	archivePath, err := Archive(stackName, archiveCfg)
	require.NoError(t, err)
	require.DirExists(t, archivePath)

	// Verify config was archived
	data, err := os.ReadFile(filepath.Join(archivePath, "config", "app.conf"))
	require.NoError(t, err)
	require.Equal(t, "config data", string(data))

	// Verify pool data was archived
	data, err = os.ReadFile(filepath.Join(archivePath, "pool_ssd", "data.db"))
	require.NoError(t, err)
	require.Equal(t, "pool data", string(data))

	// Cleanup Docker resources
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err = Cleanup(ctx, stackName, filepath.Join(root, "stacks"))
	require.NoError(t, err)

	// Docker resources should be gone
	require.False(t, testutil.ContainerRunningByProject(t, stackName))

	// But archive should still exist
	require.DirExists(t, archivePath)
	_, err = os.Stat(filepath.Join(archivePath, "config", "app.conf"))
	require.NoError(t, err, "archive should be preserved after cleanup")
}
