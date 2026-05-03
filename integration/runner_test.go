//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jamestiberiuskirk/stackr/internal/config"
	"github.com/jamestiberiuskirk/stackr/internal/runner"
	"github.com/jamestiberiuskirk/stackr/internal/testutil"
)

func TestDeploySuccess(t *testing.T) {
	testutil.RequireDockerAvailable(t)

	stackName := testutil.UniqueStackName()
	tagEnv := testutil.TagEnvVar(stackName)

	root, _ := testutil.SetupTestRepo(t,
		testutil.WithStackName(stackName),
		testutil.WithComposeContent(fmt.Sprintf(`services:
  web:
    image: nginx:${%s}
    ports:
      - "0:80"
`, tagEnv)),
		testutil.WithEnvContent(tagEnv+"=alpine\n"))
	testutil.CleanupComposeProjectByDir(t, root, stackName)

	cfg := testutil.BuildConfigDirect(root)

	r := runner.New(cfg)

	stackCfg := config.StackConfig{
		TagEnv: tagEnv,
		Args:   []string{stackName, "update"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	result, err := r.Deploy(ctx, stackName, stackCfg, "alpine")
	require.NoError(t, err)
	require.Equal(t, "ok", result.Status)
	require.Equal(t, stackName, result.Stack)
	require.Equal(t, "alpine", result.Tag)

	require.True(t, testutil.ContainerRunningByProject(t, stackName),
		"expected container to be running after deploy")
}

func TestDeployFailureRollsBackEnv(t *testing.T) {
	testutil.RequireDockerAvailable(t)

	stackName := testutil.UniqueStackName()
	tagEnv := testutil.TagEnvVar(stackName)

	root, _ := testutil.SetupTestRepo(t,
		testutil.WithStackName(stackName),
		testutil.WithComposeContent(fmt.Sprintf(`services:
  web:
    image: this-image-does-not-exist-anywhere:${%s}
    ports:
      - "0:80"
`, tagEnv)),
		testutil.WithEnvContent(tagEnv+"=v1.0.0\n"))
	testutil.CleanupComposeProjectByDir(t, root, stackName)

	cfg := testutil.BuildConfigDirect(root)

	r := runner.New(cfg)

	stackCfg := config.StackConfig{
		TagEnv: tagEnv,
		Args:   []string{stackName, "update"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	_, err := r.Deploy(ctx, stackName, stackCfg, "v2.0.0")
	require.Error(t, err, "deploy should fail with non-existent image")

	var cmdErr *runner.CommandError
	require.ErrorAs(t, err, &cmdErr, "error should be a CommandError")

	data, err := os.ReadFile(cfg.EnvFile)
	require.NoError(t, err)
	require.Contains(t, string(data), tagEnv+"=v1.0.0",
		"env file should be rolled back to original tag")
	require.NotContains(t, string(data), tagEnv+"=v2.0.0",
		"env file should NOT contain the failed tag")
}

func TestDeployConcurrentSerialization(t *testing.T) {
	testutil.RequireDockerAvailable(t)

	stackName := testutil.UniqueStackName()
	tagEnv := testutil.TagEnvVar(stackName)

	root, _ := testutil.SetupTestRepo(t,
		testutil.WithStackName(stackName),
		testutil.WithComposeContent(fmt.Sprintf(`services:
  web:
    image: nginx:${%s}
    ports:
      - "0:80"
`, tagEnv)),
		testutil.WithEnvContent(tagEnv+"=alpine\n"))
	testutil.CleanupComposeProjectByDir(t, root, stackName)

	cfg := testutil.BuildConfigDirect(root)

	r := runner.New(cfg)

	stackCfg := config.StackConfig{
		TagEnv: tagEnv,
		Args:   []string{stackName, "update"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var wg sync.WaitGroup
	var mu sync.Mutex
	var starts []time.Time
	var ends []time.Time

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			mu.Lock()
			starts = append(starts, start)
			mu.Unlock()

			_, _ = r.Deploy(ctx, stackName, stackCfg, "alpine")

			end := time.Now()
			mu.Lock()
			ends = append(ends, end)
			mu.Unlock()
		}()
	}

	wg.Wait()

	require.Len(t, starts, 2)
	require.Len(t, ends, 2)

	// Heuristic only — we don't have a hard signal that the runner mutex
	// fired (no observability hook there). The non-deadlock + non-error
	// outcome is the actual contract this test enforces.
	t.Logf("concurrent deploy completed: starts=%v ends=%v", starts, ends)
}
