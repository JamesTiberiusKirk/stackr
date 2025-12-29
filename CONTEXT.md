# Stackr Project Context

## What is Stackr?

Stackr is a standalone Go project for managing Docker Compose stacks with:
- CLI (`stackr`) for local stack management
- API daemon (`stackrd`) for remote deployments via HTTP
- Cron scheduler for running compose services on schedules
- Environment variable management with automatic rollback

## Current State

**Status**: Extracted from serverconfig repository, ready for initial release

**Completed**:
- ✅ Full Go source code (cmd/ and internal/ packages)
- ✅ Module name: `github.com/jamestiberiuskirk/stackr`
- ✅ All import paths updated
- ✅ Dockerfile for building containerized stackrd
- ✅ GitHub workflows (CI + release with semantic versioning)
- ✅ Example deployment in deploy/example/
- ✅ MIT License
- ✅ Tests passing (10/10)
- ✅ Documentation cleaned up (no run.sh references)

**Not Yet Done**:
- ⚠️ First git commit (files are staged and ready)
- ⚠️ Push to GitHub
- ⚠️ Create v0.1.0 tag to trigger first release
- ⚠️ Publish Docker image to ghcr.io/jamestiberiuskirk/stackr

## Project Structure

```
stackr/
├── cmd/
│   ├── stackr/          # CLI binary
│   └── stackrd/         # API daemon binary
├── internal/
│   ├── config/          # .stackr.yaml parsing
│   ├── cronjobs/        # Cron scheduler
│   ├── envfile/         # .env file management with snapshots
│   ├── httpapi/         # HTTP API handlers
│   ├── runner/          # Deployment orchestration
│   ├── stackcmd/        # Docker Compose command execution
│   └── watch/           # File system watcher
├── deploy/example/      # Example Docker deployment
├── docs/adrs/           # Architecture decision records
├── .github/workflows/   # CI/CD (test, lint, release, publish)
├── Dockerfile           # Multi-stage build for stackrd
├── Makefile             # Build targets
└── README.md            # User documentation
```

## Key Architecture Points

1. **No shell scripts**: Everything is pure Go, using `docker compose` CLI
2. **Environment management**: Snapshots .env before updates, restores on failure
3. **Path translation**: When using Docker socket, distinguishes between host paths (for volume mounts) and container paths (for reading files)
4. **Output capture**: Uses io.Writer interfaces for capturing stdout/stderr from compose commands
5. **Docker socket**: Container can control host Docker but needs:
   - `~/.docker/config.json` mounted for registry auth
   - `STACKR_HOST_REPO_ROOT` for volume mount paths
   - `STACKR_REPO_ROOT` for container's own filesystem paths

## Docker Image Publishing

Release workflow (`.github/workflows/release.yml`):
1. Triggered when CI workflow completes on main branch
2. Uses go-semantic-release to create version tag
3. Builds Docker image with multi-stage build
4. Pushes to ghcr.io/jamestiberiuskirk/stackr with both version tag and `latest`

## Integration with serverconfig

The serverconfig repository now:
- Uses published stackr images from GHCR
- No longer contains stackr Go code
- Maintains stack configurations in stacks/ directory
- References https://github.com/jamestiberiuskirk/stackr for documentation

## Next Steps

1. Commit and push to GitHub
2. Create v0.1.0 tag to trigger first release
3. Verify Docker image publishes to GHCR successfully
4. Update serverconfig to pull the published image

## Important Notes

- This is a standalone project, NOT part of serverconfig
- No references to run.sh (that's serverconfig-specific)
- Uses semantic versioning with "v" prefix (e.g., v0.1.0, v1.2.3)
- Docker images tagged with both version and `latest`
- All tests must pass before merge (enforced by CI)
