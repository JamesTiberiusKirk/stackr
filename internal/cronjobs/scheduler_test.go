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

	// Find jobs by service name (order is not guaranteed from map iteration)
	var scraperJob, noopJob *cronJob
	for i := range jobs {
		if jobs[i].Service == "scraper" {
			scraperJob = &jobs[i]
		} else if jobs[i].Service == "noop" {
			noopJob = &jobs[i]
		}
	}

	// Verify scraper job
	require.NotNil(t, scraperJob, "scraper job should be discovered")
	require.Equal(t, "myapp", scraperJob.Stack)
	require.Equal(t, "scraper", scraperJob.Service)
	require.Equal(t, "scraper", scraperJob.Profile)
	require.True(t, scraperJob.RunOnDeploy)
	require.Equal(t, "0 1 * * *", scraperJob.Schedule)

	// Verify noop job (manual-only with empty schedule)
	require.NotNil(t, noopJob, "noop job should be discovered")
	require.Equal(t, "myapp", noopJob.Stack)
	require.Equal(t, "noop", noopJob.Service)
	require.Equal(t, "", noopJob.Schedule)
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
