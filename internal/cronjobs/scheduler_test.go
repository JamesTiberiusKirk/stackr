package cronjobs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jamestiberiuskirk/stackr/internal/config"
)

func TestDiscoverJobsParsesScheduleProfileAndRunOnDeploy(t *testing.T) {
	t.Helper()
	stacksDir := t.TempDir()
	stackDir := filepath.Join(stacksDir, "myapp")
	require.NoError(t, os.MkdirAll(stackDir, 0o755))

	compose := `
services:
  scraper:
    profiles: ["scraper"]
    labels:
      - stackr.cron.schedule=0 1 * * *
      - stackr.cron.run_on_deploy=true
  noop:
    labels:
      stackr.cron.schedule: ""
`
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, "docker-compose.yml"), []byte(compose), 0o644))

	cfg := config.Config{StacksDir: stacksDir}
	jobs, err := discoverJobs(cfg)
	require.NoError(t, err)

	require.Len(t, jobs, 2)

	// First job should be scraper with schedule
	job := jobs[0]
	require.Equal(t, "myapp", job.Stack)
	require.Equal(t, "scraper", job.Service)
	require.Equal(t, "scraper", job.Profile)
	require.True(t, job.RunOnDeploy)
	require.Equal(t, "0 1 * * *", job.Schedule)

	// Second job should be noop with empty schedule (manual-only)
	manualJob := jobs[1]
	require.Equal(t, "myapp", manualJob.Stack)
	require.Equal(t, "noop", manualJob.Service)
	require.Equal(t, "", manualJob.Schedule)
}

func TestDiscoverJobsIgnoresInvalidRunOnDeploy(t *testing.T) {
	t.Helper()
	stacksDir := t.TempDir()
	stackDir := filepath.Join(stacksDir, "example")
	require.NoError(t, os.MkdirAll(stackDir, 0o755))

	compose := `
services:
  nightly:
    labels:
      - stackr.cron.schedule=@daily
      - stackr.cron.run_on_deploy=notabool
`
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, "docker-compose.yml"), []byte(compose), 0o644))

	cfg := config.Config{StacksDir: stacksDir}
	jobs, err := discoverJobs(cfg)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.False(t, jobs[0].RunOnDeploy)
}
