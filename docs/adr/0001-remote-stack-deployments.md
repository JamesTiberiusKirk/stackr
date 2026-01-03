# ADR 0001: Remote Stack Deployments from Git Repositories

## Status

Proposed

## Context

Currently, all stacks must be defined locally in the `stacks/` directory within the stackr repository. This creates several limitations:

1. **Tight coupling**: Application code and deployment configuration are often in separate repositories, requiring manual synchronization
2. **Version management**: Tracking which version of an application is deployed requires manual updates to local compose files or env vars
3. **Developer workflow**: Developers working on applications must separately manage deployment configs
4. **Multi-environment**: Running the same application in different environments requires duplicating stack configurations

Modern deployment platforms (Railway, Dokploy, Coolify) solve this by allowing deployments to be configured from remote Git repositories, where the application source code and deployment configuration live together.

## Decision

Implement support for **remote stacks** - stacks whose deployment configuration lives in external Git repositories alongside application source code.

### Configuration Model

**Main stackr config (`.stackr.yaml`):**
```yaml
# Configure where remote repos are cloned
remote_stacks_dir: .stackr-repos
```

**Custom remote repo config (`stacks/myapp/stackr.yaml`):**
```yaml
# Define remote stacks
remote_repo:
   repo: git@github.com:user/myapp.git
   branch: master
   path: .                          # Path within repo to .stackr-deployment.yaml
   version: ${MYAPP_VERSION}        # Variable from .env or hardcoded value
```

**Stack deployment config (`.stackr-deployment.yaml` in remote repo):**
```yaml
# Stack-specific configuration that overrides global settings
env:
   CUSTOM_VAR: value
   LOG_LEVEL: debug

stackr:
   release: tag      # "tag" or "commit"

# Override domain for this stack
domain: myapp.example.com

# Any other stack-specific overrides
```
> Note: the release is for deploying the application itself, the config (stackr-deployment and docker compose) configs from the remote repo will re reloaded/reanalysed on every commit in the repo
>> Note: when theres a new tag we need to implement a retry mechanism as the tag might exist but the image might have not been published yet


### Key Design Decisions

1. **Separate config file**: Use `.stackr-deployment.yaml` (not `.stackr.yaml`) to clearly distinguish stack-specific deployment config from main stackr config

2. **Override mechanism**: Stack deployment config can override:
   - Environment variables
   - Domain/subdomain
   - Other stack-specific settings
   - Global config remains the source of truth for system-wide settings

3. **Version resolution**:
   - `release: tag` - Checkout Git tags, version must be a valid tag (e.g., `v1.2.3`)
   - `release: commit` - Checkout specific commit hash
   - Version can reference env vars (e.g., `${MYAPP_VERSION}`) or be hardcoded

4. **Authentication**: Use system SSH keys for Git operations (simple, secure, no credential management)

5. **Storage**: Configurable directory for cloned repos (default: `.stackr-repos/`)

6. **Auto-update workflow**:
   - When `stackr myapp update` is called, pull latest from Git first
   - Check if version has changed (tag/commit)
   - If changed, redeploy
   - Supports CI/CD workflows where new tags/commits trigger redeployment

### File Structure

```
/srv/stackr/
├── .stackr.yaml           # Main config with remote_stacks
├── .env                   # MYAPP_VERSION=v1.2.3
├── stacks/                # Local stacks (existing)
│   └── monitoring/
└── .stackr-repos/         # Remote stacks (new)
    └── myapp/
        ├── .git/
        ├── .stackr-deployment.yaml
        ├── docker-compose.yml
        └── src/
```

### Implementation Components

1. **Git operations package** (`internal/git/`):
   - Clone repository
   - Pull/fetch updates
   - Checkout specific tag/commit
   - Use system SSH keys

2. **Remote stack config** (`internal/config/`):
   - Parse `remote_stacks` section
   - Parse `.stackr-deployment.yaml`
   - Merge deployment config with global config

3. **Stack discovery** (`internal/stackcmd/`):
   - Discover both local and remote stacks
   - Treat remote stacks uniformly with local stacks
   - Support all existing commands (update, backup, etc.)

4. **Update workflow**:
   - Pull latest from Git before deploying
   - Resolve version (tag/commit from env var or config)
   - Checkout correct version
   - Deploy using merged configuration

## Consequences

### Positive

- **Unified workflow**: Application and deployment config in same repo
- **Versioning**: Git tags/commits naturally track deployment versions
- **Developer experience**: Developers control deployment config alongside code
- **CI/CD friendly**: Push tag → stackr auto-deploys
- **Multi-environment**: Same repo, different configs (via version pinning)
- **Separation of concerns**: Application repos don't need stackr installation

### Negative

- **Complexity**: Added Git operations and config merging logic
- **Network dependency**: Requires Git access during deployment
- **Storage**: Cloned repos consume disk space
- **State management**: Need to track which version is currently deployed
- **Error handling**: Git operations can fail (network, auth, missing tags)

### Risks & Mitigations

| Risk | Mitigation |
|------|-----------|
| SSH key access issues | Clear documentation, fallback error messages |
| Missing tags/commits | Validate version before checkout, clear errors |
| Merge conflicts in .stackr-deployment.yaml | Fail fast with clear error message |
| Disk space from many repos | Configurable cleanup, shallow clones |
| Git operations timeout | Configurable timeouts, async operations |

## Alternatives Considered

### 1. Keep everything local (current state)
**Rejected**: Doesn't solve the coupling problem, manual synchronization remains

### 2. Support only docker-compose.yml from remote repo
**Rejected**: Doesn't allow stack-specific overrides, loses flexibility

### 3. Use .stackr.yaml in remote repo (not .stackr-deployment.yaml)
**Rejected**: Confusing - remote repos would have main stackr config when they're just one stack

### 4. Webhook-based updates instead of polling
**Deferred**: Can be added later, starting with pull-on-update is simpler

### 5. Support for multiple compose files in one repo
**Deferred**: Start with single docker-compose.yml, extend later if needed

## References

- Railway: https://docs.railway.app/
- Dokploy: https://dokploy.com/
- Coolify: https://coolify.io/

## Notes

Implementation should be done incrementally:
1. Git operations and remote stack config parsing
2. Basic clone/pull and checkout
3. Integration with existing stack commands
4. Auto-update detection and deployment
5. Error handling and edge cases
