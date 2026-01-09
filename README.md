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
- **Remote stack deployments** from external Git repositories
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

## Remote Stacks

Remote stacks allow you to deploy Docker Compose applications directly from external Git repositories, enabling your application code and deployment configuration to live together in the same repository.

### Why Use Remote Stacks?

- **Monorepo-friendly**: Keep deployment configuration with application code
- **Version coupling**: Ensure compose files match application code versions
- **CI/CD integration**: Deploy specific git tags/commits from your CI pipeline
- **Separation of concerns**: Infrastructure repo manages stack definitions, app repos contain compose files
- **Graceful degradation**: Continues using cached version if git is temporarily unreachable

### Stack Types

Stackr supports two types of stacks:

1. **Local stacks**: Traditional stacks with `docker-compose.yml` in your `stacks/` directory
2. **Remote stacks**: Stacks defined by a `stackr-repo.yml` file that points to a Git repository

### Setting Up a Remote Stack

#### 1. Create a remote stack definition

In your infrastructure repository, create a `stackr-repo.yml` file in the stack directory:

```
my-server/
├── .stackr.yaml
├── .env
└── stacks/
    ├── local-app/
    │   └── docker-compose.yml     # Local stack
    └── myapp/
        └── stackr-repo.yml         # Remote stack definition
```

**stacks/myapp/stackr-repo.yml**:
```yaml
remote_repo:
  url: git@github.com:org/myapp.git
  branch: main                      # Optional, defaults to "main"
  path: deploy                      # Optional subdirectory, defaults to "."
  release:
    type: tag                       # "tag" or "commit"
    ref: ${MYAPP_VERSION}           # Resolved from .env file
```

#### 2. Configure version in .env

```bash
# In your .env file
MYAPP_VERSION=v1.2.3
```

#### 3. Deploy the remote stack

```bash
stackr myapp update
```

Stackr will:
1. Clone the Git repository (if not already cloned)
2. Resolve `${MYAPP_VERSION}` from your `.env` file
3. Checkout the specified tag/commit
4. Deploy using the `docker-compose.yml` from the remote repository

### Remote Stack Configuration

#### Main Configuration (.stackr.yaml)

Add the remote stacks directory to your main config:

```yaml
stacks_dir: stacks
remote_stacks_dir: .stackr-repos   # Where remote repos are cloned (default)

# ... rest of your config
```

#### Per-Stack Definition (stacks/{name}/stackr-repo.yml)

```yaml
remote_repo:
  # Required: Git repository URL (SSH or HTTPS)
  url: git@github.com:org/myapp.git

  # Optional: Branch to track (default: "main")
  branch: main

  # Optional: Subdirectory containing docker-compose.yml (default: ".")
  path: deploy

  # Required: Release configuration
  release:
    # Type: "tag" for git tags, "commit" for commit hashes
    type: tag

    # Ref: Git tag, commit hash, or environment variable
    # Use ${VAR} syntax to resolve from .env file
    ref: ${MYAPP_VERSION}
```

#### Remote Deployment Config (.stackr-deployment.yaml in remote repo)

Optionally add a `.stackr-deployment.yaml` file in your remote repository to provide deployment-specific environment variables:

```yaml
env:
  LOG_LEVEL: debug
  DATABASE_HOST: postgres.internal
  FEATURE_FLAGS: experimental
```

### Environment Variable Merging

Environment variables are merged with the following priority (highest to lowest):

1. **Stack-specific env** from main `.stackr.yaml` (`env.stacks.{stackName}`)
2. **Remote deployment config** from `.stackr-deployment.yaml` in remote repo
3. **Global env** from main `.stackr.yaml` (`env.global`)
4. **Auto-provisioned vars** (STACKR_PROV_POOL_*, STACKR_PROV_DOMAIN)
5. **Custom paths** from `.stackr.yaml` (`paths.custom`)
6. **Base .env** file

This allows you to:
- Define sensible defaults in the remote repo
- Override them in your infrastructure config as needed
- Keep stack-specific overrides at the highest priority

### Sync Behavior

Remote stacks are synced in two scenarios:

1. **Every deployment**: Stackr always pulls the latest remote config
   - Updates `.stackr-deployment.yaml` on every run
   - Only changes application version if the resolved ref changes

2. **Version changes**: Application deployment only happens when:
   - The resolved git ref (tag/commit) changes in `.env`
   - A new tag is created matching the ref pattern

### Retry Logic for Image Availability

When deploying a new tag, the Docker image might not be published yet (common in CI/CD workflows). Stackr implements exponential backoff retry:

- **Max attempts**: 5
- **Delays**: 30s, 1m, 2m, 4m, 5m (up to 5 minutes max)
- **Behavior**: Retries image pull on failure, logs each attempt

This ensures deployments succeed even if the image registry is slower than your Git tags.

### Graceful Degradation

If Git operations fail (network issues, authentication, etc.):

- **Warning logged**: Git sync failure is logged with details
- **Cached version used**: Continues deployment with previously cloned repo
- **No deployment failure**: Git issues don't prevent stack operations

Only fails if:
- Repository has never been cloned
- Git ref doesn't exist in the repository

### Example Workflows

#### Deploying a specific version

```bash
# Update .env with new version
echo "MYAPP_VERSION=v2.0.0" >> .env

# Deploy the new version
stackr myapp update
```

#### CI/CD Integration

```yaml
# In your application's CI pipeline
- name: Deploy new version
  run: |
    curl -X POST https://stackr.example.com/deploy \
      -H "Authorization: Bearer $STACKR_TOKEN" \
      -d '{"stack":"myapp","tag":"${{ github.ref_name }}"}'
```

The deploy API will:
1. Update `MYAPP_VERSION` in `.env`
2. Clone/sync the Git repository
3. Checkout the tag
4. Deploy with retry logic

#### Mixing Local and Remote Stacks

```
stacks/
├── nginx/
│   └── docker-compose.yml          # Local stack
├── postgres/
│   └── docker-compose.yml          # Local stack
└── myapp/
    └── stackr-repo.yml             # Remote stack
```

All standard Stackr commands work with both types:

```bash
stackr all update              # Updates all stacks (local + remote)
stackr myapp update            # Updates just the remote stack
stackr nginx postgres update   # Updates just the local stacks
```

### Ambiguous Stack Detection

Stackr will error if a stack directory contains both `docker-compose.yml` and `stackr-repo.yml`:

```
Error: stack "myapp" has both docker-compose.yml and stackr-repo.yml - this is ambiguous, please use one or the other
```

Choose either:
- Local stack: Use `docker-compose.yml` directly
- Remote stack: Use `stackr-repo.yml` pointing to a repository

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
