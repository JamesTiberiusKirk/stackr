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
		switch jobs[i].Service {
		case "scraper":
			scraperJob = &jobs[i]
		case "noop":
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

func TestSchedulerStartStopLifecycle(t *testing.T) {
	t.Run("NoJobs", func(t *testing.T) {
		s := &Scheduler{
			cfg:  config.Config{},
			jobs: nil,
		}
		require.NoError(t, s.Start())
		s.Stop()
	})

	t.Run("DoubleStartIsIdempotent", func(t *testing.T) {
		s := &Scheduler{
			cfg:  config.Config{},
			jobs: nil,
		}
		require.NoError(t, s.Start())
		require.NoError(t, s.Start()) // second call should be a no-op
		s.Stop()
	})

	t.Run("StopOnNilScheduler", func(t *testing.T) {
		var s *Scheduler
		// Should not panic
		s.Stop()
	})

	t.Run("StartOnNilScheduler", func(t *testing.T) {
		var s *Scheduler
		require.NoError(t, s.Start())
	})

	t.Run("StopWithoutStart", func(t *testing.T) {
		s := &Scheduler{
			cfg:  config.Config{},
			jobs: nil,
		}
		// Stop without Start should not panic
		s.Stop()
	})
}

func TestDiscoverJobsEdgeCases(t *testing.T) {
	t.Run("EmptyStacksDir", func(t *testing.T) {
		stacksDir := t.TempDir()
		cfg := config.Config{StacksDir: stacksDir}
		jobs, err := discoverJobs(cfg)
		require.NoError(t, err)
		require.Empty(t, jobs)
	})

	t.Run("StackDirWithoutComposeFile", func(t *testing.T) {
		stacksDir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(stacksDir, "nocompose"), 0o755))
		cfg := config.Config{StacksDir: stacksDir}
		jobs, err := discoverJobs(cfg)
		require.NoError(t, err)
		require.Empty(t, jobs)
	})

	t.Run("MalformedYAML", func(t *testing.T) {
		stacksDir := t.TempDir()
		stackDir := filepath.Join(stacksDir, "broken")
		require.NoError(t, os.MkdirAll(stackDir, 0o755))
		require.NoError(t, os.WriteFile(
			filepath.Join(stackDir, "docker-compose.yml"),
			[]byte("{{invalid yaml content"),
			0o644,
		))
		cfg := config.Config{StacksDir: stacksDir}
		_, err := discoverJobs(cfg)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse")
	})

	t.Run("NonExistentStacksDir", func(t *testing.T) {
		cfg := config.Config{StacksDir: "/tmp/nonexistent-stackr-test-dir"}
		_, err := discoverJobs(cfg)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to read stacks dir")
	})

	t.Run("FilesInStacksDirSkipped", func(t *testing.T) {
		stacksDir := t.TempDir()
		// Create a regular file (not a dir) in stacks dir
		require.NoError(t, os.WriteFile(filepath.Join(stacksDir, "not-a-stack.txt"), []byte("hello"), 0o644))
		cfg := config.Config{StacksDir: stacksDir}
		jobs, err := discoverJobs(cfg)
		require.NoError(t, err)
		require.Empty(t, jobs)
	})
}
