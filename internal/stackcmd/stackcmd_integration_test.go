//go:build integration

package stackcmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jamestiberiuskirk/stackr/internal/testutil"
)

func TestComposeUpAndDown(t *testing.T) {
	testutil.RequireDockerAvailable(t)

	root, stackName := testutil.SetupTestRepo(t,
		testutil.WithComposeContent(`services:
  web:
    image: nginx:alpine
    ports:
      - "0:80"
`))
	testutil.CleanupComposeProjectByDir(t, root, stackName)

	cfg := testutil.BuildConfigDirect(root)

	var stdout, stderr bytes.Buffer
	manager, err := NewManagerWithWriters(cfg, &stdout, &stderr)
	require.NoError(t, err)

	// Deploy (update action)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	err = manager.Run(ctx, Options{Stacks: []string{stackName}, Update: true})
	require.NoError(t, err, "compose up failed: stdout=%s stderr=%s", stdout.String(), stderr.String())

	// Verify container is running
	require.True(t, testutil.ContainerRunningByProject(t, stackName),
		"expected container to be running after update")

	// Tear down
	stdout.Reset()
	stderr.Reset()
	manager2, err := NewManagerWithWriters(cfg, &stdout, &stderr)
	require.NoError(t, err)

	err = manager2.Run(ctx, Options{Stacks: []string{stackName}, TearDown: true})
	require.NoError(t, err, "compose down failed: stdout=%s stderr=%s", stdout.String(), stderr.String())

	// Verify container is gone
	require.False(t, testutil.ContainerRunningByProject(t, stackName),
		"expected container to be stopped after tear-down")
}

func TestEnvVarHandling(t *testing.T) {
	testutil.RequireDockerAvailable(t)

	t.Run("CustomVarsInjected", func(t *testing.T) {
		root, stackName := testutil.SetupTestRepo(t,
			testutil.WithComposeContent(`services:
  web:
    image: nginx:${MY_TAG}
`),
			testutil.WithEnvContent("MY_TAG=alpine\n"))
		testutil.CleanupComposeProjectByDir(t, root, stackName)
		cfg := testutil.BuildConfigDirect(root)

		var stdout, stderr bytes.Buffer
		manager, err := NewManagerWithWriters(cfg, &stdout, &stderr)
		require.NoError(t, err)

		// Use dry-run to check resolved config without starting containers
		ctx := context.Background()
		err = manager.Run(ctx, Options{Stacks: []string{stackName}, DryRun: true})
		require.NoError(t, err, "dry-run failed: stderr=%s", stderr.String())
		require.Contains(t, stdout.String(), "nginx:alpine",
			"expected resolved image in config output")
	})

	t.Run("AutoProvisionedVars", func(t *testing.T) {
		root, stackName := testutil.SetupTestRepo(t,
			testutil.WithComposeContent(`services:
  web:
    image: nginx:alpine
    environment:
      - DOMAIN=${STACKR_PROV_DOMAIN}
      - SSD=${STACKR_PROV_POOL_SSD}
      - HDD=${STACKR_PROV_POOL_HDD}
`),
			testutil.WithEnvContent(""))

		cfg := testutil.BuildConfigDirect(root)
		cfg.Global.HTTP.BaseDomain = "example.com"
		cfg.Global.Paths.Pools = map[string]string{
			"SSD": ".ssd_pool",
			"HDD": ".hdd_pool",
		}

		var stdout, stderr bytes.Buffer
		manager, err := NewManagerWithWriters(cfg, &stdout, &stderr)
		require.NoError(t, err)

		ctx := context.Background()
		err = manager.Run(ctx, Options{Stacks: []string{stackName}, DryRun: true})
		require.NoError(t, err, "dry-run failed: stderr=%s", stderr.String())

		output := stdout.String()
		require.Contains(t, output, stackName+".example.com")
		require.Contains(t, output, filepath.Join(root, ".ssd_pool", stackName))
		require.Contains(t, output, filepath.Join(root, ".hdd_pool", stackName))
	})

	t.Run("StackIsolation", func(t *testing.T) {
		stackA := testutil.UniqueStackName()
		stackB := testutil.UniqueStackName()
		root, _ := testutil.SetupTestRepo(t,
			testutil.WithStackName(stackA),
			testutil.WithComposeContent(`services:
  web:
    image: nginx:${STACKA_TAG}
`),
			testutil.WithExtraStack(stackB, `services:
  web:
    image: nginx:${STACKB_TAG}
`),
			testutil.WithEnvContent("STACKA_TAG=v1\nSTACKB_TAG=v2\n"))

		cfg := testutil.BuildConfigDirect(root)

		var stdout, stderr bytes.Buffer
		manager, err := NewManagerWithWriters(cfg, &stdout, &stderr)
		require.NoError(t, err)

		// Dry-run only stackA
		ctx := context.Background()
		err = manager.Run(ctx, Options{Stacks: []string{stackA}, DryRun: true})
		require.NoError(t, err, "dry-run failed: stderr=%s", stderr.String())

		output := stdout.String()
		require.Contains(t, output, "nginx:v1", "stackA should use STACKA_TAG=v1")
		require.NotContains(t, output, "nginx:v2", "stackB's tag should not appear in stackA's config")
	})

	t.Run("GlobalVarsSharedAcrossStacks", func(t *testing.T) {
		stackA := testutil.UniqueStackName()
		stackB := testutil.UniqueStackName()
		root, _ := testutil.SetupTestRepo(t,
			testutil.WithStackName(stackA),
			testutil.WithComposeContent(`services:
  web:
    image: nginx:alpine
    environment:
      - SHARED=${SHARED_KEY}
`),
			testutil.WithExtraStack(stackB, `services:
  web:
    image: nginx:alpine
    environment:
      - SHARED=${SHARED_KEY}
`),
			testutil.WithEnvContent(""))

		cfg := testutil.BuildConfigDirect(root)
		cfg.Global.Env.Global = map[string]string{"SHARED_KEY": "shared_value"}

		// Test stackA
		var stdoutA, stderrA bytes.Buffer
		managerA, err := NewManagerWithWriters(cfg, &stdoutA, &stderrA)
		require.NoError(t, err)

		ctx := context.Background()
		err = managerA.Run(ctx, Options{Stacks: []string{stackA}, DryRun: true})
		require.NoError(t, err)
		require.Contains(t, stdoutA.String(), "shared_value")

		// Test stackB
		var stdoutB, stderrB bytes.Buffer
		managerB, err := NewManagerWithWriters(cfg, &stdoutB, &stderrB)
		require.NoError(t, err)

		err = managerB.Run(ctx, Options{Stacks: []string{stackB}, DryRun: true})
		require.NoError(t, err)
		require.Contains(t, stdoutB.String(), "shared_value")
	})

	t.Run("MissingVarFailsValidation", func(t *testing.T) {
		root, stackName := testutil.SetupTestRepo(t,
			testutil.WithComposeContent(`services:
  web:
    image: nginx:alpine
    environment:
      - UNDEFINED=${UNDEFINED_VAR}
`),
			testutil.WithEnvContent(""))
		testutil.CleanupComposeProjectByDir(t, root, stackName)

		cfg := testutil.BuildConfigDirect(root)

		var stdout, stderr bytes.Buffer
		manager, err := NewManagerWithWriters(cfg, &stdout, &stderr)
		require.NoError(t, err)

		ctx := context.Background()
		err = manager.Run(ctx, Options{Stacks: []string{stackName}, Update: true})
		require.Error(t, err, "expected error for missing env var")
		require.Contains(t, err.Error(), "UNDEFINED_VAR")
	})

	t.Run("OfflineStackSkipped", func(t *testing.T) {
		stackName := testutil.UniqueStackName()
		root, _ := testutil.SetupTestRepo(t,
			testutil.WithStackName(stackName),
			testutil.WithComposeContent(`services:
  web:
    image: nginx:alpine
`),
			testutil.WithEnvContent(testutil.OfflineEnvVar(stackName)+"=true\n"))

		cfg := testutil.BuildConfigDirect(root)

		var stdout, stderr bytes.Buffer
		manager, err := NewManagerWithWriters(cfg, &stdout, &stderr)
		require.NoError(t, err)

		ctx := context.Background()
		err = manager.Run(ctx, Options{Stacks: []string{stackName}, Update: true})
		require.NoError(t, err)

		// Should not have started any containers
		require.False(t, testutil.ContainerRunningByProject(t, stackName))
	})

	t.Run("PoolPathsResolvedCorrectly", func(t *testing.T) {
		root, stackName := testutil.SetupTestRepo(t,
			testutil.WithComposeContent(`services:
  web:
    image: nginx:alpine
    environment:
      - SSD_PATH=${STACKR_PROV_POOL_SSD}
      - HDD_PATH=${STACKR_PROV_POOL_HDD}
`),
			testutil.WithEnvContent(""))
		testutil.CleanupComposeProjectByDir(t, root, stackName)

		cfg := testutil.BuildConfigDirect(root)
		cfg.Global.Paths.Pools = map[string]string{
			"SSD": ".ssd_pool",
			"HDD": ".hdd_pool",
		}

		var stdout, stderr bytes.Buffer
		manager, err := NewManagerWithWriters(cfg, &stdout, &stderr)
		require.NoError(t, err)

		// Deploy to trigger directory creation
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		err = manager.Run(ctx, Options{Stacks: []string{stackName}, Update: true})
		require.NoError(t, err, "deploy failed: stderr=%s", stderr.String())

		// Verify pool directories were created
		expectedSSD := filepath.Join(root, ".ssd_pool", stackName)
		expectedHDD := filepath.Join(root, ".hdd_pool", stackName)

		_, err = os.Stat(expectedSSD)
		require.NoError(t, err, "SSD pool dir should be created at %s", expectedSSD)

		_, err = os.Stat(expectedHDD)
		require.NoError(t, err, "HDD pool dir should be created at %s", expectedHDD)
	})

	t.Run("AbsolutePoolPathsPreserved", func(t *testing.T) {
		root, stackName := testutil.SetupTestRepo(t,
			testutil.WithComposeContent(`services:
  web:
    image: nginx:alpine
    environment:
      - SSD=${STACKR_PROV_POOL_SSD}
`),
			testutil.WithEnvContent(""))

		absPool := filepath.Join(t.TempDir(), "abs-ssd-pool")

		cfg := testutil.BuildConfigDirect(root)
		cfg.Global.Paths.Pools = map[string]string{
			"SSD": absPool,
		}

		var stdout, stderr bytes.Buffer
		manager, err := NewManagerWithWriters(cfg, &stdout, &stderr)
		require.NoError(t, err)

		ctx := context.Background()
		err = manager.Run(ctx, Options{Stacks: []string{stackName}, DryRun: true})
		require.NoError(t, err, "dry-run failed: stderr=%s", stderr.String())

		// The pool path should use the absolute path, not be joined with root
		require.Contains(t, stdout.String(), filepath.Join(absPool, stackName))
	})

	t.Run("LegacyStorageVarsSetFromPools", func(t *testing.T) {
		root, stackName := testutil.SetupTestRepo(t,
			testutil.WithComposeContent(`services:
  web:
    image: nginx:alpine
    environment:
      - HDD=${STACK_STORAGE_HDD}
      - SSD=${STACK_STORAGE_SSD}
`),
			testutil.WithEnvContent(""))

		cfg := testutil.BuildConfigDirect(root)
		cfg.Global.Paths.Pools = map[string]string{
			"SSD": ".ssd_pool",
			"HDD": ".hdd_pool",
		}

		var stdout, stderr bytes.Buffer
		manager, err := NewManagerWithWriters(cfg, &stdout, &stderr)
		require.NoError(t, err)

		ctx := context.Background()
		err = manager.Run(ctx, Options{Stacks: []string{stackName}, DryRun: true})
		require.NoError(t, err, "dry-run failed: stderr=%s", stderr.String())

		output := stdout.String()
		require.Contains(t, output, filepath.Join(root, ".ssd_pool", stackName))
		require.Contains(t, output, filepath.Join(root, ".hdd_pool", stackName))
	})

	t.Run("CustomPathVarsFromConfig", func(t *testing.T) {
		root, stackName := testutil.SetupTestRepo(t,
			testutil.WithComposeContent(`services:
  web:
    image: nginx:alpine
    environment:
      - DATA=${MY_DATA_DIR}
      - CACHE=${MY_CACHE_DIR}
`),
			testutil.WithEnvContent(""))

		cfg := testutil.BuildConfigDirect(root)
		cfg.Global.Paths.Custom = map[string]string{
			"MY_DATA_DIR":  "/opt/data",
			"MY_CACHE_DIR": "./cache",
		}

		var stdout, stderr bytes.Buffer
		manager, err := NewManagerWithWriters(cfg, &stdout, &stderr)
		require.NoError(t, err)

		ctx := context.Background()
		err = manager.Run(ctx, Options{Stacks: []string{stackName}, DryRun: true})
		require.NoError(t, err, "dry-run failed: stderr=%s", stderr.String())

		output := stdout.String()
		require.Contains(t, output, "/opt/data")
		require.Contains(t, output, "./cache")
	})

	t.Run("HostRepoRootPathTranslation", func(t *testing.T) {
		root, _ := testutil.SetupTestRepo(t,
			testutil.WithComposeContent(`services:
  web:
    image: nginx:alpine
`),
			testutil.WithEnvContent(""))

		cfg := testutil.BuildConfigDirect(root)
		cfg.HostRepoRoot = "/host/path/to/repo"

		// Manager should accept a different HostRepoRoot without error
		var stdout, stderr bytes.Buffer
		manager, err := NewManagerWithWriters(cfg, &stdout, &stderr)
		require.NoError(t, err)
		require.Equal(t, "/host/path/to/repo", manager.cfg.HostRepoRoot)
		require.Equal(t, root, manager.cfg.RepoRoot)
	})
}

func TestGetVarsAppendsToEnvFile(t *testing.T) {
	testutil.RequireDockerAvailable(t)

	root, stackName := testutil.SetupTestRepo(t,
		testutil.WithComposeContent(`services:
  web:
    image: nginx:alpine
    environment:
      - APP_PORT=${APP_PORT}
      - DB_HOST=${DB_HOST}
      - DOMAIN=${STACKR_PROV_DOMAIN}
`),
		testutil.WithEnvContent("APP_PORT=8080\n"))

	cfg := testutil.BuildConfigDirect(root)
	cfg.Global.HTTP.BaseDomain = "example.com"

	var stdout, stderr bytes.Buffer
	manager, err := NewManagerWithWriters(cfg, &stdout, &stderr)
	require.NoError(t, err)

	ctx := context.Background()
	err = manager.Run(ctx, Options{Stacks: []string{stackName}, GetVars: true})
	require.NoError(t, err, "get-vars failed: stderr=%s", stderr.String())

	data, err := os.ReadFile(cfg.EnvFile)
	require.NoError(t, err)
	content := string(data)

	// Existing var should be unchanged
	require.Contains(t, content, "APP_PORT=8080")

	// Missing non-auto-provisioned var should be appended as stub
	require.Contains(t, content, "DB_HOST=")

	// Auto-provisioned var should NOT be appended
	require.NotContains(t, content, "STACKR_PROV_DOMAIN=")

	// Section marker should be present
	require.Contains(t, content, "###### "+stackName+" vars #####")
}

func TestBackupStack(t *testing.T) {
	t.Run("BacksUpConfigDirectories", func(t *testing.T) {
		root, stackName := testutil.SetupTestRepo(t,
			testutil.WithStackDirs("config", "dashboards", "dynamic"))

		// Write some test data into the config dirs
		stackDir := filepath.Join(root, "stacks", stackName)
		testutil.WriteFile(t, filepath.Join(stackDir, "config", "app.conf"), "config data")
		testutil.WriteFile(t, filepath.Join(stackDir, "dashboards", "dash.json"), "dashboard data")
		testutil.WriteFile(t, filepath.Join(stackDir, "dynamic", "routes.yml"), "routes data")

		cfg := testutil.BuildConfigDirect(root)
		cfg.Global.Paths.BackupDir = "./backups"

		var stdout, stderr bytes.Buffer
		manager, err := NewManagerWithWriters(cfg, &stdout, &stderr)
		require.NoError(t, err)

		ctx := context.Background()
		err = manager.Run(ctx, Options{Stacks: []string{stackName}, Backup: true})
		require.NoError(t, err)

		// Find the backup dir
		backupBase := filepath.Join(root, "backups")
		entries, err := os.ReadDir(backupBase)
		require.NoError(t, err)
		require.Len(t, entries, 1, "should have one timestamped backup dir")

		backupDir := filepath.Join(backupBase, entries[0].Name(), stackName)

		// Verify all config dirs were backed up
		data, err := os.ReadFile(filepath.Join(backupDir, "config", "app.conf"))
		require.NoError(t, err)
		require.Equal(t, "config data", string(data))

		data, err = os.ReadFile(filepath.Join(backupDir, "dashboards", "dash.json"))
		require.NoError(t, err)
		require.Equal(t, "dashboard data", string(data))

		data, err = os.ReadFile(filepath.Join(backupDir, "dynamic", "routes.yml"))
		require.NoError(t, err)
		require.Equal(t, "routes data", string(data))
	})

	t.Run("BacksUpPoolVolumes", func(t *testing.T) {
		root, stackName := testutil.SetupTestRepo(t)

		// Create pool dirs with data
		ssdPool := filepath.Join(root, ".ssd_pool", stackName)
		hddPool := filepath.Join(root, ".hdd_pool", stackName)
		testutil.WriteFile(t, filepath.Join(ssdPool, "db.sqlite"), "ssd data")
		testutil.WriteFile(t, filepath.Join(hddPool, "archive.tar"), "hdd data")

		cfg := testutil.BuildConfigDirect(root)
		cfg.Global.Paths.BackupDir = "./backups"
		cfg.Global.Paths.Pools = map[string]string{
			"SSD": ".ssd_pool",
			"HDD": ".hdd_pool",
		}

		var stdout, stderr bytes.Buffer
		manager, err := NewManagerWithWriters(cfg, &stdout, &stderr)
		require.NoError(t, err)

		ctx := context.Background()
		err = manager.Run(ctx, Options{Stacks: []string{stackName}, Backup: true})
		require.NoError(t, err)

		backupBase := filepath.Join(root, "backups")
		entries, err := os.ReadDir(backupBase)
		require.NoError(t, err)
		require.Len(t, entries, 1)

		backupDir := filepath.Join(backupBase, entries[0].Name(), stackName)

		data, err := os.ReadFile(filepath.Join(backupDir, "pool_ssd", "db.sqlite"))
		require.NoError(t, err)
		require.Equal(t, "ssd data", string(data))

		data, err = os.ReadFile(filepath.Join(backupDir, "pool_hdd", "archive.tar"))
		require.NoError(t, err)
		require.Equal(t, "hdd data", string(data))
	})

	t.Run("SkipsMissingDirectories", func(t *testing.T) {
		root, stackName := testutil.SetupTestRepo(t,
			testutil.WithStackDirs("config"))

		stackDir := filepath.Join(root, "stacks", stackName)
		testutil.WriteFile(t, filepath.Join(stackDir, "config", "app.conf"), "data")
		// No dashboards/ or dynamic/ directories

		cfg := testutil.BuildConfigDirect(root)
		cfg.Global.Paths.BackupDir = "./backups"

		var stdout, stderr bytes.Buffer
		manager, err := NewManagerWithWriters(cfg, &stdout, &stderr)
		require.NoError(t, err)

		ctx := context.Background()
		err = manager.Run(ctx, Options{Stacks: []string{stackName}, Backup: true})
		require.NoError(t, err, "backup should succeed even with missing dirs")

		backupBase := filepath.Join(root, "backups")
		entries, err := os.ReadDir(backupBase)
		require.NoError(t, err)
		require.Len(t, entries, 1)

		backupDir := filepath.Join(backupBase, entries[0].Name(), stackName)

		// config/ should exist
		_, err = os.Stat(filepath.Join(backupDir, "config", "app.conf"))
		require.NoError(t, err)

		// dashboards/ and dynamic/ should NOT exist (they weren't there)
		_, err = os.Stat(filepath.Join(backupDir, "dashboards"))
		require.True(t, os.IsNotExist(err))
		_, err = os.Stat(filepath.Join(backupDir, "dynamic"))
		require.True(t, os.IsNotExist(err))
	})

	t.Run("DryRunDoesNotCopy", func(t *testing.T) {
		root, stackName := testutil.SetupTestRepo(t,
			testutil.WithStackDirs("config"))

		stackDir := filepath.Join(root, "stacks", stackName)
		testutil.WriteFile(t, filepath.Join(stackDir, "config", "app.conf"), "data")

		cfg := testutil.BuildConfigDirect(root)
		cfg.Global.Paths.BackupDir = "./backups"

		var stdout, stderr bytes.Buffer
		manager, err := NewManagerWithWriters(cfg, &stdout, &stderr)
		require.NoError(t, err)

		ctx := context.Background()
		err = manager.Run(ctx, Options{Stacks: []string{stackName}, Backup: true, DryRun: true})
		require.NoError(t, err)

		// Backup dir should NOT be created
		backupBase := filepath.Join(root, "backups")
		_, err = os.Stat(backupBase)
		require.True(t, os.IsNotExist(err), "backup dir should not exist in dry-run mode")
	})

	t.Run("BackupDirFallsBackToRoot", func(t *testing.T) {
		// When BackupDir is empty, absolutePath resolves it to the repo root.
		// This means backups are created directly under the repo root with timestamps.
		// Verify the behavior: no error, backup created under root.
		root, stackName := testutil.SetupTestRepo(t,
			testutil.WithStackDirs("config"))

		stackDir := filepath.Join(root, "stacks", stackName)
		testutil.WriteFile(t, filepath.Join(stackDir, "config", "app.conf"), "data")

		cfg := testutil.BuildConfigDirect(root)
		cfg.Global.Paths.BackupDir = "" // Empty resolves to root

		var stdout, stderr bytes.Buffer
		manager, err := NewManagerWithWriters(cfg, &stdout, &stderr)
		require.NoError(t, err)

		ctx := context.Background()
		err = manager.Run(ctx, Options{Stacks: []string{stackName}, Backup: true})
		require.NoError(t, err, "backup should succeed when BackupDir resolves to root")
	})

	t.Run("TimestampedDirectoryCreated", func(t *testing.T) {
		root, stackName := testutil.SetupTestRepo(t,
			testutil.WithStackDirs("config"))

		stackDir := filepath.Join(root, "stacks", stackName)
		testutil.WriteFile(t, filepath.Join(stackDir, "config", "app.conf"), "data")

		cfg := testutil.BuildConfigDirect(root)
		cfg.Global.Paths.BackupDir = "./backups"

		// First backup
		var stdout1, stderr1 bytes.Buffer
		m1, err := NewManagerWithWriters(cfg, &stdout1, &stderr1)
		require.NoError(t, err)

		ctx := context.Background()
		err = m1.Run(ctx, Options{Stacks: []string{stackName}, Backup: true})
		require.NoError(t, err)

		// Brief pause so timestamp differs
		time.Sleep(1100 * time.Millisecond)

		// Second backup
		var stdout2, stderr2 bytes.Buffer
		m2, err := NewManagerWithWriters(cfg, &stdout2, &stderr2)
		require.NoError(t, err)

		err = m2.Run(ctx, Options{Stacks: []string{stackName}, Backup: true})
		require.NoError(t, err)

		// Should have two different timestamped dirs
		backupBase := filepath.Join(root, "backups")
		entries, err := os.ReadDir(backupBase)
		require.NoError(t, err)
		require.Len(t, entries, 2, "should have two separate backup directories")
		require.NotEqual(t, entries[0].Name(), entries[1].Name())
	})
}
