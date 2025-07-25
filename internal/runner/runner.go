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

		if service.Build != nil {
			err := buildImage(ctx, cli, service)
			if err != nil {
				return fmt.Errorf("error building new image for service %s: %w", service.Name, err)
			}

			if service.Image == "" {
				service.Image = service.Name
			}
			fmt.Printf("Built image: %s\n", service.Image)
		} else {
			fmt.Printf("Pulling image: %s\n", service.Image)
			reader, err := cli.ImagePull(ctx, service.Image, image.PullOptions{})
			if err != nil {
				return fmt.Errorf("pull image %s: %w", service.Image, err)
			}

			io.Copy(io.Discard, reader)
			reader.Close()
		}

		config, hostConfig, netConfig, err := composeconvert.TranslateServiceConfigToContainerConfig(service)
		if err != nil {
			return fmt.Errorf("translate service %s config: %w", service.Name, err)
		}

		resp, err := cli.ContainerCreate(ctx, config, hostConfig, netConfig, nil, service.Name)
		if err != nil {
			return fmt.Errorf("create container %s: %w", service.Name, err)
		}

		fmt.Printf("Starting container %s (ID: %s)\n", service.Name, resp.ID[:12])

		if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
			return fmt.Errorf("start container %s (ID: %s): %w", service.Name, resp.ID[:12], err)
		}
	}

	return nil
}
