package runner

import (
	"context"
	"fmt"
	"io"

	"github.com/JamesTiberiusKirk/stackr/internal/composeconvert"
	"github.com/compose-spec/compose-go/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
)

func Run(ctx context.Context, cli *client.Client, stackConfig *types.Project) error {
	for _, service := range stackConfig.Services {
		fmt.Printf("\nPreparing service: %s\n", service.Name)

		fmt.Printf("Pulling image: %s\n", service.Image)
		reader, err := cli.ImagePull(ctx, service.Image, image.PullOptions{})
		if err != nil {
			return fmt.Errorf("pull image %s: %w", service.Image, err)
		}
		// defer reader.Close()
		io.Copy(io.Discard, reader)
		reader.Close()

		// if err := prettyprint.PrintPullProgress(reader); err != nil {
		// 	return fmt.Errorf("print pull progress: %w", err)
		// }

		config, hostConfig, netConfig, err := composeconvert.TranslateServiceConfigToContainerConfig(service)
		if err != nil {
			return fmt.Errorf("translate service %s: %w", service.Name, err)
		}

		resp, err := cli.ContainerCreate(ctx, config, hostConfig, netConfig, nil, service.Name)
		if err != nil {
			return fmt.Errorf("create container %s: %w", service.Name, err)
		}

		fmt.Printf("Starting container %s (%s)\n", service.Name, resp.ID[:12])

		if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
			return fmt.Errorf("start container %s: %w", service.Name, err)
		}
	}

	return nil
}
