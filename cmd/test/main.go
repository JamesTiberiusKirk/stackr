package main

import (
	"context"
	"log"

	"github.com/JamesTiberiusKirk/stackr/internal/composeconvert"
	"github.com/JamesTiberiusKirk/stackr/internal/runner"
	"github.com/docker/docker/client"
)

func main() {
	project, err := composeconvert.LoadComposeStack(composeconvert.LoadComposeProjectOptions{
		DockerFilePath:    "./cmd/test/docker-compose.yml",
		PullEnvFromSystem: true,
	})
	if err != nil {
		log.Fatal("error loading compose project %w", err)
	}

	cli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		log.Fatal("error getting docker client %w", err)
	}

	ctx := context.Background()

	if err := runner.Run(ctx, cli, project); err != nil {
		log.Fatal(err)
	}
}
