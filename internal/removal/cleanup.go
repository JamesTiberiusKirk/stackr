package removal

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Cleanup removes all Docker resources for a stack
// Uses docker compose down with volume removal
func Cleanup(ctx context.Context, stack string, stacksDir string) error {
	composePath := filepath.Join(stacksDir, stack, "docker-compose.yml")

	// Check if compose file still exists
	// If not, we need to use docker CLI directly to clean by project label
	if _, err := os.Stat(composePath); err != nil {
		if os.IsNotExist(err) {
			log.Printf("compose file gone for %s, cleaning by project label", stack)
			return cleanupByProjectLabel(ctx, stack)
		}
		return fmt.Errorf("failed to check compose file: %w", err)
	}

	// Compose file exists, use docker compose down
	log.Printf("running docker compose down for stack %s", stack)
	return dockerComposeDown(ctx, composePath)
}

// dockerComposeDown runs docker compose down with volume removal
func dockerComposeDown(ctx context.Context, composePath string) error {
	cmd := exec.CommandContext(ctx, "docker", "compose",
		"-f", composePath,
		"down",
		"--volumes",        // Remove volumes
		"--remove-orphans", // Clean orphaned containers
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose down failed: %w\nOutput: %s", err, string(output))
	}

	log.Printf("docker compose down completed: %s", strings.TrimSpace(string(output)))
	return nil
}

// cleanupByProjectLabel cleans resources when compose file is gone
// Uses docker CLI to find and remove resources by project label
func cleanupByProjectLabel(ctx context.Context, stack string) error {
	// Remove containers
	if err := removeContainers(ctx, stack); err != nil {
		return fmt.Errorf("failed to remove containers: %w", err)
	}

	// Remove volumes
	if err := removeVolumes(ctx, stack); err != nil {
		return fmt.Errorf("failed to remove volumes: %w", err)
	}

	// Remove networks
	if err := removeNetworks(ctx, stack); err != nil {
		return fmt.Errorf("failed to remove networks: %w", err)
	}

	return nil
}

func removeContainers(ctx context.Context, stack string) error {
	// List containers
	listCmd := exec.CommandContext(ctx, "docker", "ps", "-aq",
		"--filter", fmt.Sprintf("label=com.docker.compose.project=%s", stack))

	output, err := listCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to list containers: %w", err)
	}

	containerIDs := strings.Fields(strings.TrimSpace(string(output)))
	if len(containerIDs) == 0 {
		log.Printf("no containers found for stack %s", stack)
		return nil
	}

	// Remove containers
	args := append([]string{"rm", "-f"}, containerIDs...)
	rmCmd := exec.CommandContext(ctx, "docker", args...)
	if output, err := rmCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to remove containers: %w\nOutput: %s", err, string(output))
	}

	log.Printf("removed %d containers for stack %s", len(containerIDs), stack)
	return nil
}

func removeVolumes(ctx context.Context, stack string) error {
	// List volumes
	listCmd := exec.CommandContext(ctx, "docker", "volume", "ls", "-q",
		"--filter", fmt.Sprintf("label=com.docker.compose.project=%s", stack))

	output, err := listCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to list volumes: %w", err)
	}

	volumeNames := strings.Fields(strings.TrimSpace(string(output)))
	if len(volumeNames) == 0 {
		log.Printf("no volumes found for stack %s", stack)
		return nil
	}

	// Remove volumes
	args := append([]string{"volume", "rm", "-f"}, volumeNames...)
	rmCmd := exec.CommandContext(ctx, "docker", args...)
	if output, err := rmCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to remove volumes: %w\nOutput: %s", err, string(output))
	}

	log.Printf("removed %d volumes for stack %s", len(volumeNames), stack)
	return nil
}

func removeNetworks(ctx context.Context, stack string) error {
	// List networks
	listCmd := exec.CommandContext(ctx, "docker", "network", "ls", "-q",
		"--filter", fmt.Sprintf("label=com.docker.compose.project=%s", stack))

	output, err := listCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to list networks: %w", err)
	}

	networkIDs := strings.Fields(strings.TrimSpace(string(output)))
	if len(networkIDs) == 0 {
		log.Printf("no networks found for stack %s", stack)
		return nil
	}

	// Remove networks
	args := append([]string{"network", "rm"}, networkIDs...)
	rmCmd := exec.CommandContext(ctx, "docker", args...)
	if output, err := rmCmd.CombinedOutput(); err != nil {
		// Networks may fail to remove if still in use - log but don't fail
		log.Printf("warning: failed to remove some networks for stack %s: %s", stack, string(output))
		return nil
	}

	log.Printf("removed %d networks for stack %s", len(networkIDs), stack)
	return nil
}
