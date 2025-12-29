package cronjobs

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// CleanupOldContainers removes old cron job containers, keeping the last N per service
func CleanupOldContainers(retention int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// List all containers with name pattern: *-cron-*
	cmd := exec.CommandContext(ctx, "docker", "ps", "-a",
		"--filter", "name=-cron-",
		"--format", "{{.Names}}")

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to list containers: %w", err)
	}

	containerNames := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(containerNames) == 0 || containerNames[0] == "" {
		return nil // No containers to clean
	}

	// Group containers by stack-service
	containersByService := make(map[string][]string)
	for _, name := range containerNames {
		// Parse: mystack-scraper-cron-1735392000
		parts := strings.Split(name, "-cron-")
		if len(parts) != 2 {
			continue
		}
		serviceKey := parts[0] // "mystack-scraper"
		containersByService[serviceKey] = append(containersByService[serviceKey], name)
	}

	// For each service, sort by timestamp and remove old containers
	var removed []string
	for _, containers := range containersByService {
		if len(containers) <= retention {
			continue // Don't exceed retention limit
		}

		// Sort by timestamp (newest first)
		sort.Slice(containers, func(i, j int) bool {
			return containers[i] > containers[j]
		})

		// Remove containers beyond retention
		toRemove := containers[retention:]
		for _, containerName := range toRemove {
			rmCmd := exec.CommandContext(ctx, "docker", "rm", containerName)
			if err := rmCmd.Run(); err != nil {
				log.Printf("failed to remove container %s: %v", containerName, err)
				continue
			}
			removed = append(removed, containerName)
		}
	}

	if len(removed) > 0 {
		log.Printf("cleaned up %d old cron containers: %v", len(removed), removed)
	}

	return nil
}
