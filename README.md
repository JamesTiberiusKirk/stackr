# Stackr

Stackr is a declarative Docker Compose stack deployment system designed for home server environments where your entire infrastructure is defined as code in version control.

**Key Philosophy:**
- **Everything in Git, Secrets in .env**: All configuration lives in your Git repository, with only secrets stored in a single `.env` file
- **Environment Variable Validation**: Define required variables in docker-compose.yaml, and Stackr validates they exist before deployment
- **Automated Path Provisioning**: Automatically provisions storage paths, domains, and custom environment variables for each stack
- **Single Source of Truth**: Your Docker Compose files define what needs to exist; Stackr ensures it does

Stackr provides:

- **CLI** for managing Docker Compose stacks with environment automation
- **HTTP API** for remote deployments (CI/CD integration)
- **Cron scheduler** for running compose services on schedules
- **File watcher** for automatic configuration reloads
- **Environment management** with rollback support

## Features

- Manage multiple Docker Compose stacks from a single configuration
- Automated environment variable provisioning (storage paths, domains, etc.)
- Deploy API for CI/CD pipelines to trigger stack updates
- Schedule compose services using cron expressions via labels
- Watch configuration files and automatically reload
- Roll back failed deployments automatically

## Installation

### Setup Process

1. **Create your repository structure**:
   ```
   my-server/
   ├── .stackr.yaml          # Stackr configuration
   ├── .env                  # Secrets (not in git)
   └── stacks/               # Your compose stacks
       ├── myapp/
       │   └── docker-compose.yml
       └── stackr/
           └── docker-compose.yml
   ```

2. **Install the Stackr CLI**:
```sh
# Linux (amd64)
wget https://github.com/jamestiberiuskirk/stackr/releases/latest/download/stackr-linux-amd64
chmod +x stackr-linux-amd64
sudo mv stackr-linux-amd64 /usr/local/bin/stackr

# Linux (arm64)
wget https://github.com/jamestiberiuskirk/stackr/releases/latest/download/stackr-linux-arm64
chmod +x stackr-linux-arm64
sudo mv stackr-linux-arm64 /usr/local/bin/stackr

# macOS (Apple Silicon)
wget https://github.com/jamestiberiuskirk/stackr/releases/latest/download/stackr-darwin-arm64
chmod +x stackr-darwin-arm64
sudo mv stackr-darwin-arm64 /usr/local/bin/stackr

# Or using Go
go install github.com/jamestiberiuskirk/stackr/cmd/stackr@latest
```

3. **Run your stacks**:
 ```sh
 stackr myapp update
 ```

The intended workflow is to organize each service as a "stack" (a folder containing a docker-compose.yml file), then use the Stackr CLI to manage deployments with automatic environment provisioning.

## Quick Start

### 1. Configure your repository

Create a `.stackr.yaml` file in your stack repository root:

```yaml
stacks_dir: stacks               # Directory containing stack folders

cron:
  profile: cron                  # Profile name for cron-only services
  enable_file_logs: true         # Enable file-based logging for cron jobs
  logs_dir: logs/cron            # Directory for cron log files
  docker_container_retention: 5  # Keep last N cron containers per service (does NOT clean up log files)

http:
  base_domain: example.local     # Base domain for HTTP services

paths:
  backup_dir: ./backups
  pools:
    SSD: .vols_ssd/stack_volumes
    HDD: .vols_hdd/stack_volumes
  custom:
    MEDIA_STORAGE: /mnt/media    # Custom environment variables

# Optional: Deployment configuration per stack
deploy:
  myapp:
    tag_env: MYAPP_IMAGE_TAG     # Environment variable for image tag
    args: ["myapp", "update"]    # Command to run for deployment

# Optional: Inject environment variables
env:
  global:
    LOG_LEVEL: info              # Global env vars for all stacks
  stacks:
    myapp:
      DEBUG: "true"              # Stack-specific env vars
```

Most configuration belongs in your docker-compose.yml files. Image tags should be in your `.env` file so Stackr can auto-update them during deployments.

Create a `.env` file for secrets:

```bash
# Stack-specific secrets
MYAPP_IMAGE_TAG=v1.0.0
DATABASE_PASSWORD=secret
```

### 2. CLI Usage

```bash
# Update a stack
stackr myapp update

# Update all stacks
stackr all update

# Dry run to see what would happen
stackr myapp --dry-run update

# Get environment variables for a stack
stackr myapp get-vars

# Run arbitrary command with stack environment
stackr myapp vars-only -- env | grep MYAPP

# Run docker compose commands directly
stackr myapp compose up -d
stackr myapp compose logs -f
```

## API Reference

### Deploy Endpoint

Trigger a stack deployment:

```bash
curl -X POST http://localhost:9000/deploy \
  -H "Authorization: Bearer $STACKR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"stack":"myapp","tag":"v1.2.3"}'
```

**Request:**
- `stack` (string): Stack name to deploy
- `tag` (string): Image tag to deploy

**Response (200 OK):**
```json
{
  "status": "ok",
  "stack": "myapp",
  "tag": "v1.2.3",
  "previous_tag": "v1.2.2",
  "stdout": "..."
}
```

**Response (500 Error):**
```json
{
  "error": "deployment failed",
  "stderr": "...",
  "stdout": "..."
}
```

On failure, the previous tag is automatically restored in the environment file.

#### Controlling Auto-Deployment

You can disable auto-deployment for specific stacks using the `stackr.deploy.auto` label:

```yaml
services:
  app:
    image: myapp:latest
    labels:
      - stackr.deploy.auto=false  # Disable auto-deployment
```

Or reference an environment variable:

```yaml
services:
  app:
    image: myapp:latest
    labels:
      stackr.deploy.auto: ${MYAPP_AUTODEPLOY}  # Control via .env file
```

Then in your `.env` file:
```bash
MYAPP_AUTODEPLOY=true  # Set to false to disable auto-deployment
```

When auto-deployment is disabled, the deploy endpoint will return:
```json
{
  "error": "auto-deployment is disabled for this stack"
}
```

This is useful for:
- Temporarily preventing deployments during maintenance
- Requiring manual approval for critical services
- Controlling deployments per environment using .env variables

### Health Check

```bash
curl http://localhost:9000/healthz
```

Returns: `{"status":"ok"}`

## Scheduled Jobs (Cron)

Schedule Docker Compose services using labels:

```yaml
services:
  scraper:
    image: myapp/scraper:latest
    profiles:
      - cron
    labels:
      - stackr.cron.schedule=0 2 * * *           # Run at 2 AM daily
      - stackr.cron.run_on_deploy=true           # Also run on stackr startup
```

Stackr will:
1. Discover services with `stackr.cron.schedule` labels
2. Parse the cron expression (minute hour day month weekday)
3. Execute the service at scheduled times using `docker compose run`
4. Automatically reload schedules when compose files change

### Manually Running Cron Jobs

You can manually trigger cron jobs without waiting for the schedule:

```bash
# Run with default command from compose file
stackr mystack run-cron scraper

# Run with custom command
stackr mystack run-cron scraper -- /app/scraper.py --verbose --full-scan
```

**Manual-only jobs** (no automatic schedule):
```yaml
services:
  manual-backup:
    image: myapp/backup:latest
    labels:
      - stackr.cron.schedule=  # Empty value = manual-only
```

This is useful for:
- Testing cron jobs during development
- Running maintenance tasks on-demand
- Executing backups manually
- Debugging with verbose flags
- Jobs that should never run automatically

The manual execution uses the same infrastructure as scheduled runs (timestamped containers, logging to `logs/cron/`).

## Environment Variables

### CLI

- `STACKR_REPO_ROOT`: Path to repository (defaults to current directory if not set)
- `STACKR_CONFIG_FILE`: Path to .stackr.yaml (defaults to `.stackr.yaml` in repo root)
- `STACKR_ENV_FILE`: Path to .env file (configurable in `.stackr.yaml`, defaults to `.env`)
- `STACKR_STACKS_DIR`: Override stacks directory (configurable in `.stackr.yaml`)

### API Daemon (stackrd)

Required:
- `STACKR_TOKEN`: Bearer token for API authentication
- `STACKR_REPO_ROOT`: Path to repository root

Optional:
- `STACKR_HOST`: Bind address (default: `0.0.0.0`)
- `STACKR_PORT`: Listen port (default: `9000`)
- `STACKR_ENV_FILE`: Path to .env file (default: `.env`)
- `STACKR_CONFIG_FILE`: Path to .stackr.yaml (default: `.stackr.yaml`)
- `STACKR_HOST_REPO_ROOT`: Host path when using Docker socket (for volume mounts)

## CI/CD Integration

### GitHub Actions Example

```yaml
- name: Deploy to production
  env:
    STACKR_ENDPOINT: https://stackr.example.com/deploy
    STACKR_TOKEN: ${{ secrets.STACKR_TOKEN }}
  run: |
    curl -sSf -X POST "$STACKR_ENDPOINT" \
      -H "Authorization: Bearer $STACKR_TOKEN" \
      -H "Content-Type: application/json" \
      -d "{\"stack\":\"myapp\",\"tag\":\"${{ steps.version.outputs.tag }}\"}"
```

## Configuration Reference

### .stackr.yaml

```yaml
# Stack directory (relative or absolute)
stacks_dir: stacks

# Cron configuration
cron:
  profile: cron                  # Profile for cron-only services
  enable_file_logs: true         # Enable file-based logging for cron jobs
  logs_dir: logs/cron            # Directory for cron log files
  docker_container_retention: 5  # Keep last N cron containers per service (does NOT clean up log files)

# HTTP configuration
http:
  base_domain: localhost         # Domain for STACKR_PROV_DOMAIN

# Path provisioning
paths:
  backup_dir: ./backups          # Backup directory path
  pools:
    SSD: .vols_ssd               # SSD storage pool (STACKR_PROV_POOL_SSD)
    HDD: .vols_hdd               # HDD storage pool (STACKR_PROV_POOL_HDD)
  custom:
    MEDIA_STORAGE: /mnt/media    # Custom path variables

# Optional: Deployment configuration per stack
deploy:
  myapp:
    tag_env: MYAPP_IMAGE_TAG     # Environment variable holding image tag
    args: ["myapp", "update"]    # Command to run for deployment

# Optional: Environment variable injection
env:
  global:
    LOG_LEVEL: info              # Global env vars for all stacks
  stacks:
    myapp:
      DEBUG: "true"              # Stack-specific env vars
```

## Development

### Run Tests

```bash
go test ./...
```

### Linting

```bash
make lint
```

### Build

```bash
make build
```

### Docker Build

```bash
make docker-build
```

## License

MIT
