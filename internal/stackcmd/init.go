package stackcmd

import (
	"fmt"
	"os"
	"path/filepath"
)

const stackrConfigTemplate = `# Stackr configuration file
stacks_dir: stacks

# Cron configuration
cron:
  profile: cron
  enable_file_logs: true
  logs_dir: logs/cron
  container_retention: 5

# HTTP configuration
http:
  base_domain: localhost

# Path provisioning
paths:
  backup_dir: /mnt/hdd/backups
  pools:
    # custom values
    SSD: /mnt/ssd/stack_volumes
    HDD: /mnt/hdd/stack_volumes

# Optional: Environment variable injection
env:
  global: {}
  stacks: {}
`

const envTemplate = `# Stackr environment file
# Add your secrets and configuration here

# Stackr API token for remote deployments
STACKR_TOKEN=changeme

# Stack image tags
# STACKR_IMAGE_TAG=latest
# TRAEFIK_IMAGE_TAG=v3.0
`

const stackrComposeTemplate = `services:
  stackrd:
    image: ghcr.io/jamestiberiuskirk/stackrd:latest
    restart: unless-stopped
    environment:
      - STACKR_TOKEN=${STACKR_TOKEN}
      - STACKR_REPO_ROOT=/stackr
      - STACKR_HOST_REPO_ROOT=${PWD}
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - ${PWD}:/stackr:ro
      - ${STACK_STORAGE_HDD}/backups:/stackr/backups
    networks:
      - web
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.stackr.rule=Host(` + "`" + `${STACKR_PROV_DOMAIN}` + "`" + `)"
      - "traefik.http.routers.stackr.entrypoints=web"
      - "traefik.http.services.stackr.loadbalancer.server.port=9000"

networks:
  web:
    external: true
`

const traefikComposeTemplate = `services:
  traefik:
    image: traefik:latest
    restart: unless-stopped
    command:
      - "--api.insecure=true"
      - "--providers.docker=true"
      - "--providers.docker.exposedbydefault=false"
      - "--entrypoints.web.address=:80"
      - "--entrypoints.websecure.address=:443"
    ports:
      - "80:80"
      - "443:443"
      - "8080:8080"  # Traefik dashboard
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - ${STACK_STORAGE_HDD}/acme:/acme
    networks:
      - web
    labels:
      - "traefik.enable=true"

networks:
  web:
    name: web
    driver: bridge
`

const readmeTemplate = `# Stackr Project

This project was initialized with Stackr.

## Getting Started

1. Review and customize .stackr.yaml configuration
2. Add your secrets to .env file (not committed to git)
3. Customize the example stacks in stacks/ directory
4. Deploy your stacks:

` + "```" + `bash
# Start Traefik (reverse proxy)
stackr traefik update

# Start Stackr API daemon
stackr stackr update

# View all stacks
stackr all get-vars
` + "```" + `

## Project Structure

` + "```" + `
.
├── .stackr.yaml       # Stackr configuration
├── .env               # Secrets (not in git)
├── .gitignore        # Git ignore rules
├── README.md         # This file
└── stacks/           # Your Docker Compose stacks
    ├── stackr/       # Stackr API daemon
    └── traefik/      # Traefik reverse proxy
` + "```" + `

## Documentation

See the official Stackr documentation for more information:
https://github.com/jamestiberiuskirk/stackr
`

const gitignoreAddition = `
# Stackr
.env
.env.local
`

// RunInit initializes a new Stackr project in the current directory
func RunInit() error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	fmt.Printf("Initializing Stackr project in %s\n\n", cwd)

	// Check if .stackr.yaml already exists
	stackrConfigPath := filepath.Join(cwd, ".stackr.yaml")
	if _, err := os.Stat(stackrConfigPath); err == nil {
		return fmt.Errorf(".stackr.yaml already exists in this directory")
	}

	// Create .stackr.yaml
	fmt.Println("Creating .stackr.yaml...")
	if err := os.WriteFile(stackrConfigPath, []byte(stackrConfigTemplate), 0644); err != nil {
		return fmt.Errorf("failed to create .stackr.yaml: %w", err)
	}

	// Create .env file if it doesn't exist
	envPath := filepath.Join(cwd, ".env")
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		fmt.Println("Creating .env...")
		if err := os.WriteFile(envPath, []byte(envTemplate), 0644); err != nil {
			return fmt.Errorf("failed to create .env: %w", err)
		}
	} else {
		fmt.Println("Skipping .env (already exists)")
	}

	// Create stacks directory
	stacksDir := filepath.Join(cwd, "stacks")
	fmt.Println("Creating stacks/ directory...")
	if err := os.MkdirAll(stacksDir, 0755); err != nil {
		return fmt.Errorf("failed to create stacks directory: %w", err)
	}

	// Create stackr stack
	stackrDir := filepath.Join(stacksDir, "stackr")
	fmt.Println("Creating stacks/stackr/...")
	if err := os.MkdirAll(stackrDir, 0755); err != nil {
		return fmt.Errorf("failed to create stackr stack directory: %w", err)
	}
	stackrComposePath := filepath.Join(stackrDir, "docker-compose.yml")
	if err := os.WriteFile(stackrComposePath, []byte(stackrComposeTemplate), 0644); err != nil {
		return fmt.Errorf("failed to create stackr docker-compose.yml: %w", err)
	}

	// Create traefik stack
	traefikDir := filepath.Join(stacksDir, "traefik")
	fmt.Println("Creating stacks/traefik/...")
	if err := os.MkdirAll(traefikDir, 0755); err != nil {
		return fmt.Errorf("failed to create traefik stack directory: %w", err)
	}
	traefikComposePath := filepath.Join(traefikDir, "docker-compose.yml")
	if err := os.WriteFile(traefikComposePath, []byte(traefikComposeTemplate), 0644); err != nil {
		return fmt.Errorf("failed to create traefik docker-compose.yml: %w", err)
	}

	// Create README.md
	readmePath := filepath.Join(cwd, "README.md")
	if _, err := os.Stat(readmePath); os.IsNotExist(err) {
		fmt.Println("Creating README.md...")
		if err := os.WriteFile(readmePath, []byte(readmeTemplate), 0644); err != nil {
			return fmt.Errorf("failed to create README.md: %w", err)
		}
	} else {
		fmt.Println("Skipping README.md (already exists)")
	}

	// Update or create .gitignore
	gitignorePath := filepath.Join(cwd, ".gitignore")
	if _, err := os.Stat(gitignorePath); os.IsNotExist(err) {
		fmt.Println("Creating .gitignore...")
		if err := os.WriteFile(gitignorePath, []byte(gitignoreAddition), 0644); err != nil {
			return fmt.Errorf("failed to create .gitignore: %w", err)
		}
	} else {
		fmt.Println("Appending to .gitignore...")
		f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("failed to open .gitignore: %w", err)
		}
		defer func() { _ = f.Close() }()
		if _, err := f.WriteString(gitignoreAddition); err != nil {
			return fmt.Errorf("failed to append to .gitignore: %w", err)
		}
	}

	fmt.Println("\n✓ Stackr project initialized successfully!")
	fmt.Println("\nNext steps:")
	fmt.Println("  1. Review and customize .stackr.yaml")
	fmt.Println("  2. Update STACKR_TOKEN in .env file")
	fmt.Println("  3. Start your stacks:")
	fmt.Println("     stackr traefik update")
	fmt.Println("     stackr stackr update")
	fmt.Println("\nFor more information, see README.md")

	return nil
}
