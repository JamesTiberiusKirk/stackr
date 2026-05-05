//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jamestiberiuskirk/stackr/internal/cronjobs"
	"github.com/jamestiberiuskirk/stackr/internal/testutil"
)

func TestExecuteJobManually(t *testing.T) {
	testutil.RequireDockerAvailable(t)

	root, stackName := testutil.SetupTestRepo(t,
		testutil.WithComposeContent(`services:
  worker:
    image: alpine:latest
    command: ["echo", "hello-from-cron"]
    labels:
      stackr.cron.schedule: "* * * * *"
    profiles:
      - cron
`),
		testutil.WithEnvContent(""))

	t.Cleanup(func() {
		cmd := exec.Command("docker", "compose",
			"-f", filepath.Join(root, "stacks", stackName, "docker-compose.yml"),
			"--profile", "cron",
			"down", "--volumes", "--remove-orphans", "--timeout", "5")
		_ = cmd.Run()
	})

	cfg := testutil.BuildConfigDirect(root)
	cfg.Global.Cron.EnableFileLogs = true
	cfg.Global.Cron.LogsDir = "logs/cron"

	err := cronjobs.ExecuteJobManually(cfg, stackName, "worker", nil, nil)
	require.NoError(t, err)

	logsDir := filepath.Join(root, "logs", "cron", stackName)
	entries, err := os.ReadDir(logsDir)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(entries), 1, "expected at least one log file")

	hasContent := false
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(logsDir, entry.Name()))
		if err == nil && len(data) > 0 {
			hasContent = true
			break
		}
	}
	require.True(t, hasContent, "expected at least one log file with content")
}

func TestExecuteJobManuallyWithCustomCommand(t *testing.T) {
	testutil.RequireDockerAvailable(t)

	root, stackName := testutil.SetupTestRepo(t,
		testutil.WithComposeContent(`services:
  worker:
    image: alpine:latest
    command: ["echo", "default-output"]
    labels:
      stackr.cron.schedule: "* * * * *"
    profiles:
      - cron
`),
		testutil.WithEnvContent(""))

	t.Cleanup(func() {
		cmd := exec.Command("docker", "compose",
			"-f", filepath.Join(root, "stacks", stackName, "docker-compose.yml"),
			"--profile", "cron",
			"down", "--volumes", "--remove-orphans", "--timeout", "5")
		_ = cmd.Run()
	})

	cfg := testutil.BuildConfigDirect(root)
	cfg.Global.Cron.EnableFileLogs = true
	cfg.Global.Cron.LogsDir = "logs/cron"

	err := cronjobs.ExecuteJobManually(cfg, stackName, "worker", []string{"echo", "custom-output"}, nil)
	require.NoError(t, err)
}

func TestCleanupOldContainers(t *testing.T) {
	testutil.RequireDockerAvailable(t)

	prefix := testutil.UniqueStackName() + "-worker-cron-"

	for i := 0; i < 5; i++ {
		timestamp := time.Now().Unix() + int64(i)
		name := prefix + strings.Replace(
			time.Unix(timestamp, 0).Format("20060102150405"), " ", "", -1)

		testutil.CreateStoppedContainer(t, name, "alpine:latest")

		time.Sleep(10 * time.Millisecond)
	}

	err := cronjobs.CleanupOldContainers(3)
	require.NoError(t, err)

	remaining := testutil.CountContainersByName(t, prefix)

	require.LessOrEqual(t, remaining, 3,
		"expected at most 3 containers to remain after cleanup, got %d", remaining)
}
