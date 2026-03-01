//go:build integration

package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/stretchr/testify/require"
	tc "github.com/testcontainers/testcontainers-go"

	"github.com/jamestiberiuskirk/stackr/internal/config"
)

// UniqueStackName generates a unique stack name for test isolation.
// Each test gets its own Docker Compose project name, preventing
// race conditions when packages run in parallel.
func UniqueStackName() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic("failed to generate random bytes: " + err.Error())
	}
	return "st" + hex.EncodeToString(b)
}

// TagEnvVar returns the IMAGE_TAG env var name for a given stack,
// matching the convention used by the deploy handler.
func TagEnvVar(stackName string) string {
	return strings.ToUpper(stackName) + "_IMAGE_TAG"
}

// OfflineEnvVar returns the OFFLINE env var name for a given stack,
// matching the convention used by isStackOffline.
func OfflineEnvVar(stackName string) string {
	return strings.ToUpper(stackName) + "_OFFLINE"
}

// RequireDockerAvailable skips the test if the Docker provider is not healthy.
// Uses testcontainers' provider health check rather than shelling out to docker CLI.
func RequireDockerAvailable(t *testing.T) {
	t.Helper()
	tc.SkipIfProviderIsNotHealthy(t)
}

// DockerClient returns a testcontainers Docker API client for inspecting
// containers, networks, and volumes. The client is closed when the test finishes.
func DockerClient(t *testing.T) *tc.DockerClient {
	t.Helper()
	ctx := context.Background()
	client, err := tc.NewDockerClientWithOpts(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// ContainerRunningByProject returns true if any container with the given
// compose project label is in "running" state, using the Docker API.
func ContainerRunningByProject(t *testing.T, project string) bool {
	t.Helper()
	ctx := context.Background()
	client := DockerClient(t)

	containers, err := client.ContainerList(ctx, container.ListOptions{
		Filters: filters.NewArgs(
			filters.Arg("label", "com.docker.compose.project="+project),
		),
	})
	if err != nil {
		t.Logf("ContainerRunningByProject: list error: %v", err)
		return false
	}

	for _, c := range containers {
		if c.State == "running" {
			return true
		}
	}
	return false
}

// ContainerExistsByProject returns true if any container (running or stopped)
// with the given compose project label exists.
func ContainerExistsByProject(t *testing.T, project string) bool {
	t.Helper()
	ctx := context.Background()
	client := DockerClient(t)

	containers, err := client.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", "com.docker.compose.project="+project),
		),
	})
	if err != nil {
		return false
	}
	return len(containers) > 0
}

// NetworkExistsByProject returns true if any network with the given
// compose project label exists.
func NetworkExistsByProject(t *testing.T, project string) bool {
	t.Helper()
	ctx := context.Background()
	client := DockerClient(t)

	networks, err := client.NetworkList(ctx, network.ListOptions{
		Filters: filters.NewArgs(
			filters.Arg("label", "com.docker.compose.project="+project),
		),
	})
	if err != nil {
		return false
	}
	return len(networks) > 0
}

// VolumeExistsByProject returns true if any volume with the given
// compose project label exists.
func VolumeExistsByProject(t *testing.T, project string) bool {
	t.Helper()
	ctx := context.Background()
	client := DockerClient(t)

	volumes, err := client.VolumeList(ctx, volume.ListOptions{
		Filters: filters.NewArgs(
			filters.Arg("label", "com.docker.compose.project="+project),
		),
	})
	if err != nil {
		return false
	}
	return len(volumes.Volumes) > 0
}

// ContainerImageByProject returns the image name for the first running container
// in the given compose project, or empty string if not found.
func ContainerImageByProject(t *testing.T, project string) string {
	t.Helper()
	ctx := context.Background()
	client := DockerClient(t)

	containers, err := client.ContainerList(ctx, container.ListOptions{
		Filters: filters.NewArgs(
			filters.Arg("label", "com.docker.compose.project="+project),
		),
	})
	if err != nil || len(containers) == 0 {
		return ""
	}
	return containers[0].Image
}

// ContainerEnvByProject returns the environment variables for the first
// running container in the given compose project, using docker inspect.
func ContainerEnvByProject(t *testing.T, project string) map[string]string {
	t.Helper()
	ctx := context.Background()
	client := DockerClient(t)

	containers, err := client.ContainerList(ctx, container.ListOptions{
		Filters: filters.NewArgs(
			filters.Arg("label", "com.docker.compose.project="+project),
		),
	})
	if err != nil || len(containers) == 0 {
		return nil
	}

	inspect, err := client.ContainerInspect(ctx, containers[0].ID)
	if err != nil {
		return nil
	}

	env := make(map[string]string)
	for _, e := range inspect.Config.Env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			env[parts[0]] = parts[1]
		}
	}
	return env
}

// CountContainersByName returns the number of containers (running or stopped)
// matching the given name filter using the Docker API.
func CountContainersByName(t *testing.T, nameFilter string) int {
	t.Helper()
	ctx := context.Background()
	client := DockerClient(t)

	containers, err := client.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("name", nameFilter),
		),
	})
	if err != nil {
		return 0
	}
	return len(containers)
}

// CreateStoppedContainer creates a stopped container with the given name using
// the Docker API directly (via testcontainers client). Useful for testing cleanup logic.
func CreateStoppedContainer(t *testing.T, name, image string) string {
	t.Helper()
	ctx := context.Background()
	client := DockerClient(t)

	resp, err := client.ContainerCreate(ctx,
		&container.Config{Image: image, Cmd: []string{"echo", "test"}},
		nil, nil, nil, name)
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
	})

	return resp.ID
}

// RepoOption configures a test repo created by SetupTestRepo.
type RepoOption func(*repoConfig)

type repoConfig struct {
	stackName      string
	composeContent string
	envContent     string
	yamlContent    string
	extraStacks    map[string]string // stackName -> composeContent
	extraDirs      []string          // relative dirs to create inside the stack
}

// WithStackName sets the primary stack name (default: "testapp").
func WithStackName(name string) RepoOption {
	return func(c *repoConfig) { c.stackName = name }
}

// WithComposeContent sets the docker-compose.yml content for the primary stack.
func WithComposeContent(content string) RepoOption {
	return func(c *repoConfig) { c.composeContent = content }
}

// WithEnvContent sets the .env file content.
func WithEnvContent(content string) RepoOption {
	return func(c *repoConfig) { c.envContent = content }
}

// WithYAMLConfig sets the .stackr.yaml content.
func WithYAMLConfig(content string) RepoOption {
	return func(c *repoConfig) { c.yamlContent = content }
}

// WithExtraStack adds an additional stack with the given compose content.
func WithExtraStack(name, composeContent string) RepoOption {
	return func(c *repoConfig) {
		if c.extraStacks == nil {
			c.extraStacks = make(map[string]string)
		}
		c.extraStacks[name] = composeContent
	}
}

// WithStackDirs creates extra subdirectories inside the primary stack dir.
func WithStackDirs(dirs ...string) RepoOption {
	return func(c *repoConfig) { c.extraDirs = dirs }
}

// SetupTestRepo creates a temporary directory structured as a stackr repo.
// It returns the repo root path and the stack name used. The directory is
// cleaned up when the test finishes. By default, a unique stack name is
// generated to prevent Docker Compose project name collisions when test
// packages run in parallel.
func SetupTestRepo(t *testing.T, opts ...RepoOption) (string, string) {
	t.Helper()
	rc := &repoConfig{
		stackName:      UniqueStackName(),
		composeContent: MinimalComposeYAML("web", "nginx:alpine"),
		envContent:     "",
		yamlContent:    "",
	}
	for _, opt := range opts {
		opt(rc)
	}

	root := t.TempDir()

	// Create stacks dir and primary stack
	stackDir := filepath.Join(root, "stacks", rc.stackName)
	require.NoError(t, os.MkdirAll(stackDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(stackDir, "docker-compose.yml"),
		[]byte(rc.composeContent),
		0o644,
	))

	// Create extra directories inside primary stack
	for _, dir := range rc.extraDirs {
		require.NoError(t, os.MkdirAll(filepath.Join(stackDir, dir), 0o755))
	}

	// Create extra stacks
	for name, content := range rc.extraStacks {
		dir := filepath.Join(root, "stacks", name)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(
			filepath.Join(dir, "docker-compose.yml"),
			[]byte(content),
			0o644,
		))
	}

	// Write .env
	require.NoError(t, os.WriteFile(
		filepath.Join(root, ".env"),
		[]byte(rc.envContent),
		0o644,
	))

	// Write .stackr.yaml if provided
	if rc.yamlContent != "" {
		require.NoError(t, os.WriteFile(
			filepath.Join(root, ".stackr.yaml"),
			[]byte(rc.yamlContent),
			0o644,
		))
	}

	return root, rc.stackName
}

// BuildConfig constructs a config.Config pointing at a test repo root.
// It loads from the filesystem using config.LoadForCLI for realistic behaviour.
func BuildConfig(t *testing.T, root string) config.Config {
	t.Helper()
	cfg, err := config.LoadForCLI(root)
	require.NoError(t, err)
	return cfg
}

// BuildConfigDirect constructs a config.Config with explicit fields,
// bypassing the filesystem loader. Useful when you need precise control.
func BuildConfigDirect(root string) config.Config {
	return config.Config{
		RepoRoot:     root,
		HostRepoRoot: root,
		EnvFile:      filepath.Join(root, ".env"),
		StacksDir:    filepath.Join(root, "stacks"),
		Global: config.GlobalConfig{
			Path:   filepath.Join(root, ".stackr.yaml"),
			Stacks: filepath.Join(root, "stacks"),
			HTTP:   config.HTTPConfig{BaseDomain: "localhost"},
			Paths: config.PathsConfig{
				BackupDir: "./backups",
				Pools:     map[string]string{},
				Custom:    map[string]string{},
			},
			Env: config.EnvConfig{
				Global: map[string]string{},
				Stacks: map[string]map[string]string{},
			},
			Cron: config.CronConfig{
				DefaultProfile:     "cron",
				EnableFileLogs:     true,
				LogsDir:            "logs/cron",
				ContainerRetention: 5,
			},
		},
	}
}

// MinimalComposeYAML returns a minimal docker-compose.yml for a single service.
func MinimalComposeYAML(serviceName, image string) string {
	return fmt.Sprintf(`services:
  %s:
    image: %s
`, serviceName, image)
}

// CleanupComposeProject runs docker compose down for a compose file.
// Registered as t.Cleanup so resources are removed when the test finishes.
func CleanupComposeProject(t *testing.T, composePath string) {
	t.Helper()
	t.Cleanup(func() {
		cmd := exec.Command("docker", "compose", "-f", composePath,
			"down", "--volumes", "--remove-orphans", "--timeout", "5")
		_ = cmd.Run()
	})
}

// CleanupComposeProjectByDir finds docker-compose.yml under stacks/<name> and cleans up.
func CleanupComposeProjectByDir(t *testing.T, root, stack string) {
	t.Helper()
	composePath := filepath.Join(root, "stacks", stack, "docker-compose.yml")
	CleanupComposeProject(t, composePath)
}

// WriteFile is a test helper that writes content to a file, creating parent dirs.
func WriteFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// ReadFile is a test helper that reads a file's content.
func ReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}
