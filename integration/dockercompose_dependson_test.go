package integrationtest

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/JamesTiberiusKirk/stackr/internal/composeconvert"
	"github.com/JamesTiberiusKirk/stackr/internal/runner"
	"github.com/compose-spec/compose-go/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/teris-io/shortid"
)

func TestCompose_DependsOn(t *testing.T) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)

	tests := []struct {
		name       string
		composeYML string
		// If not present, it will just not run
		assertComposeStackFunc func(t *testing.T, testID string, project *types.Project)
		// If not present, the stack and assertion func will not be run
		assertRunFunc func(t *testing.T, info container.InspectResponse, sid string)
	}{
		{
			name:       "Basic_order_test",
			composeYML: "test_docker_compose/dependson/basic_order.yml",
			assertComposeStackFunc: func(t *testing.T, testID string, project *types.Project) {
				assert.Equal(t, 2, len(project.Services))
				assert.True(t, strings.Contains(project.Services[0].Name, "srv2"))
				assert.True(t, strings.Contains(project.Services[1].Name, "srv1"))
			},
			// assertRunFunc: func(t *testing.T, c container.InspectResponse, sid string) {
			// },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			sid, err := shortid.Generate()
			require.NoError(t, err)
			sid = strings.ToLower(sid)

			project, err := composeconvert.LoadComposeStack(ctx, composeconvert.LoadComposeProjectOptions{
				NamePrefix:        "stackr_test-",
				NameSuffix:        "-" + sid,
				DockerComposePath: tt.composeYML,
				PullEnvFromSystem: false,
			})
			require.NoError(t, err, "Error from load compose stack")

			if tt.assertComposeStackFunc != nil {
				tt.assertComposeStackFunc(t, sid, project)
			}

			t.Cleanup(func() {
				ctx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cleanupCancel()
				for _, svc := range project.Services {

					_, err := cli.ContainerInspect(ctx, svc.Name)
					if err == nil {
						err = cli.ContainerRemove(ctx, svc.Name, container.RemoveOptions{Force: true})
						require.NoError(t, err, "[CLEANUP] Error removing container")
					}

					_, err = cli.ImageInspect(ctx, svc.Image)
					if err == nil {
						_, err = cli.ImageRemove(ctx, svc.Image, image.RemoveOptions{Force: true, PruneChildren: true})
						require.NoError(t, err, "[CLEANUP] Error removing image")
					}

					for _, vol := range svc.Volumes {
						if vol.Type == "bind" && vol.Source != "" {
							err = os.RemoveAll(vol.Source)
							require.NoError(t, err, "[CLEANUP] Error removing bind folder")
							require.NoDirExists(t, vol.Source, "[CLEANUP] Error directory still exists")
						}
					}
				}
			})

			if tt.assertRunFunc != nil {
				require.NoError(t, runner.Run(ctx, cli, project), "Error running stack")
				time.Sleep(2 * time.Second)

				info, err := cli.ContainerInspect(ctx, project.Services[0].Name)
				require.NoError(t, err, "Error inspecting container")

				tt.assertRunFunc(t, info, sid)
			}
		})
	}
}
