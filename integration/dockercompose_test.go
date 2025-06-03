package integrationtest

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/JamesTiberiusKirk/stackr/internal/composeconvert"
	"github.com/JamesTiberiusKirk/stackr/internal/runner"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComposeFeatureIsolation(t *testing.T) {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)

	tests := []struct {
		name       string
		composeYML string
		assertFunc func(t *testing.T, info container.InspectResponse)
	}{
		{
			name:       "Environment variables",
			composeYML: "test_docker_compose/env.yml",
			assertFunc: func(t *testing.T, c container.InspectResponse) {
				env := c.Config.Env
				assert.Contains(t, env, "FOO=bar")
				assert.Contains(t, env, "HELLO=world")
			},
		},
		{
			name:       "Labels",
			composeYML: "test_docker_compose/labels.yml",
			assertFunc: func(t *testing.T, c container.InspectResponse) {
				labels := c.Config.Labels
				assert.Equal(t, "true", labels["com.example.test"])
				assert.Equal(t, "v1", labels["version"])
			},
		},
		{
			name:       "Ports",
			composeYML: "test_docker_compose/ports.yml",
			assertFunc: func(t *testing.T, c container.InspectResponse) {
				bindings, ok := c.HostConfig.PortBindings["80/tcp"]
				require.True(t, ok)

				assert.Contains(t, bindings, nat.PortBinding{HostIP: "", HostPort: "8081"})
			},
		},
		{
			name:       "Volumes",
			composeYML: "test_docker_compose/volumes.yml",
			assertFunc: func(t *testing.T, c container.InspectResponse) {
				found := false
				for _, m := range c.Mounts {
					if strings.HasSuffix(m.Source, ".docker-mount") &&
						m.Destination == "/usr/share/nginx/html" &&
						m.Type == "bind" {
						found = true
					}
				}
				assert.True(t, found, "volume /usr/share/nginx/html bound from .docker-mount")
			},
		},
		{
			name:       "Hostname",
			composeYML: "test_docker_compose/hostname.yml",
			assertFunc: func(t *testing.T, c container.InspectResponse) {
				assert.Equal(t, "hostname-test", c.Config.Hostname)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			project, err := composeconvert.LoadComposeStack(composeconvert.LoadComposeProjectOptions{
				DockerFilePath:    tt.composeYML,
				PullEnvFromSystem: true,
			})
			require.NoError(t, err)

			t.Cleanup(func() {
				for _, svc := range project.Services {
					_ = cli.ContainerRemove(ctx, svc.Name, container.RemoveOptions{Force: true})
					_, _ = cli.ImageRemove(ctx, svc.Image, image.RemoveOptions{Force: true, PruneChildren: true})
					for _, vol := range svc.Volumes {
						if vol.Type == "bind" && vol.Source != "" {
							_ = os.RemoveAll(vol.Source)
						}
					}
				}
			})

			require.NoError(t, runner.Run(ctx, cli, project))
			time.Sleep(2 * time.Second)

			info, err := cli.ContainerInspect(ctx, project.Services[0].Name)
			require.NoError(t, err)

			tt.assertFunc(t, info)
		})
	}
}
