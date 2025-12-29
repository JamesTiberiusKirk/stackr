# Stackr Example Deployment

This directory contains an example Docker Compose deployment for Stackr.

## Prerequisites

- Docker and Docker Compose installed
- A serverconfig repository (or similar stack configuration)
- Logged into GHCR for pulling private images

## Setup

1. Copy the example environment file:
   ```bash
   cp .env.example .env
   ```

2. Edit `.env` and configure:
   - `STACKR_TOKEN`: Generate a secure token for API authentication
   - `STACKR_HOST_REPO_ROOT`: Absolute path to your serverconfig repository

3. Start Stackr:
   ```bash
   docker compose up -d
   ```

4. Check health:
   ```bash
   curl http://localhost:9000/healthz
   ```

## Configuration

The example deployment:
- Exposes Stackr API on port 9000
- Mounts your serverconfig repository
- Mounts Docker socket for container management
- Mounts Docker config for registry authentication
- Uses latest Stackr image from GHCR

## Production Considerations

For production deployments:
1. Use a specific version tag instead of `latest`
2. Configure TLS/SSL (use Traefik or nginx reverse proxy)
3. Set up proper secrets management
4. Configure monitoring and logging
5. Review security settings (restrict API access, network policies)

## API Usage

Deploy a stack:
```bash
curl -X POST http://localhost:9000/deploy \
  -H "Authorization: Bearer $STACKR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"stack":"mystack","tag":"v1.0.0"}'
```

Health check:
```bash
curl http://localhost:9000/healthz
```

See the main [README](../../README.md) for full API documentation.
