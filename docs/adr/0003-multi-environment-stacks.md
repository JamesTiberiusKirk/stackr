# ADR 0003: Multi-Environment Stacks

## Status

Proposed (future work)

## Context

Currently, a stack is a single running instance — one set of containers, one image tag, one domain. This creates friction for real-world development workflows:

1. **No staging**: To test a new version, you either deploy to production or manually spin up a separate stack with a different name, duplicating the compose file and config.
2. **No PR previews**: Railway and similar platforms offer ephemeral environments per pull request. Developers push a branch, CI deploys a preview, reviewers see it live, and it's torn down on merge. Stackr has no concept of this.
3. **Environment parity**: Production and staging should use the same compose definition with different config (image tags, domains, resource limits, env vars). Currently this requires maintaining separate stacks.

## Decision

Introduce **environments** as a first-class concept. A single stack definition can have multiple running environments, each with its own configuration, domain, and lifecycle.

### Configuration Model

```yaml
# stacks/api/stackr/config.yaml
environments:
  production:
    domain: api.company.dev
  staging:
    domain: api-staging.company.dev
  pr:
    ephemeral: true
    domain_template: "api-pr-{id}.company.dev"
    auto_teardown: on_merge
```

If no `environments` section is defined, the stack behaves as today — a single implicit environment. This preserves full backward compatibility.

### How Environments Work

**Each environment gets:**
- Its own Docker Compose project name: `{stack}-{environment}` (e.g. `api-production`, `api-staging`, `api-pr-123`)
- Its own env var namespace: tag env becomes `{STACK}_{ENV}_IMAGE_TAG` (e.g. `API_STAGING_IMAGE_TAG`)
- Its own auto-provisioned domain: from the environment's `domain` or `domain_template`
- Its own pool storage paths: `{pool}/{stack}-{environment}/`
- Its own deployment history and cron executions in the state store
- Isolation from other environments of the same stack — they don't share Docker networks or volumes

**They share:**
- The same compose file(s)
- The same stackr/config.yaml (minus the per-environment overrides)
- The same permission model (access to the stack grants access to all its environments, unless environment-scoped permissions are added later)

### Environment Types

**Persistent environments** (production, staging):
- Always running
- Deployed via API or CI
- Manual teardown only

**Ephemeral environments** (PR previews):
- Created on demand via the deploy API with an `environment` and `id` parameter
- Automatically torn down based on `auto_teardown` policy:
  - `on_merge` — stackr watches for PR merge/close (requires GitHub/GitLab webhook or polling)
  - `ttl: 24h` — auto-destroyed after a time limit
  - `manual` — must be explicitly torn down
- Domain provisioned from `domain_template` with `{id}` replaced by the PR number or branch name

### Deploy API

```json
POST /api/v1/stacks/api/deploy
{
  "tag": "v1.2.3",
  "environment": "staging"
}
```

For ephemeral environments:

```json
POST /api/v1/stacks/api/deploy
{
  "tag": "pr-123-abc456",
  "environment": "pr",
  "environment_id": "123"
}
```

If `environment` is omitted, defaults to `production` (or the single implicit environment for stacks without the `environments` config).

### Environment-Specific Env Vars

Env vars can be scoped per environment in `.stackr.yaml`:

```yaml
env:
  stacks:
    api:
      API_DB_NAME: myapp
  stack_environments:
    api.staging:
      API_DB_NAME: myapp_staging
    api.pr:
      API_DB_NAME: myapp_pr_{id}
```

Or in the stack's own config:

```yaml
# stacks/api/stackr/config.yaml
environments:
  production:
    env:
      API_LOG_LEVEL: info
  staging:
    env:
      API_LOG_LEVEL: debug
  pr:
    env:
      API_LOG_LEVEL: debug
```

### State Model Impact

The `deployments`, `cron_executions`, and `events` tables gain an `environment` column:

```sql
ALTER TABLE deployments ADD COLUMN environment TEXT NOT NULL DEFAULT 'production';
ALTER TABLE cron_executions ADD COLUMN environment TEXT NOT NULL DEFAULT 'production';
ALTER TABLE events ADD COLUMN environment TEXT;
```

The API filters by environment: `GET /api/v1/deployments?stack=api&environment=staging`

### Permission Model Impact

Initially, permissions are per-stack and cover all environments. A user with `stacks.deploy` for the `api` stack can deploy to any environment.

Future refinement could add environment-scoped permissions:

```yaml
users:
  - username: dave
    stacks:
      api:
        production: ["stacks.view"]
        staging: ["stacks.view", "stacks.deploy"]
        pr: ["stacks.view", "stacks.deploy"]
```

But this is not required for the initial implementation.

### Listing Environments

```
GET /api/v1/stacks/api/environments
```

Returns all environments for a stack with their status (running, stopped, pending teardown), current tag, domain, and age (for ephemeral environments).

### Cleanup

Ephemeral environments require cleanup:
- Containers and networks (`docker compose down --volumes --remove-orphans`)
- Pool storage directories
- State store records (mark as torn down, don't delete — keep history)
- DNS/Traefik labels (handled by removing the compose project)

A cleanup job runs periodically to enforce TTL policies and check for environments that should have been torn down.

## Consequences

### What becomes easier

- **Development workflow**: Developers get staging and PR preview environments with minimal config
- **Environment parity**: Same compose file, different config — no stack duplication
- **CI/CD integration**: Deploy to staging on push to main, create PR environment on PR open, teardown on merge
- **Testing**: QA can review changes in isolation before production

### What becomes harder

- **Resource management**: Each environment runs its own set of containers. Ephemeral environments can consume resources if not cleaned up properly.
- **Domain management**: Wildcard DNS or dynamic DNS needed for PR environments. Traefik handles this natively with labels.
- **Complexity**: The stack model goes from "one thing running" to "N things running." Every part of the system (API, UI, permissions, state) needs to be environment-aware.
- **Storage**: Each environment gets its own pool directories, multiplying disk usage.

## Alternatives Considered

- **Separate stacks per environment**: Current approach. Works but duplicates config and doesn't scale for ephemeral environments.
- **Docker Compose profiles**: Could be used to define environment variants within one compose file, but profiles are for service selection, not config variation. Doesn't handle separate domains or isolation.
- **Branch-based deployments**: Deploy whatever branch is checked out. Too implicit — doesn't give clear control over what's running where.
- **Kubernetes namespaces**: The obvious solution in K8s-land, but stackr targets Docker Compose on single servers (for now).

## Implementation Notes

This feature depends on:
- ADR-0002 (state store, API, auth) being implemented first
- The deploy API being environment-aware from the start (even if only `production` exists initially)

The state store schema should include the `environment` column from day one to avoid migrations later. The API should accept an `environment` parameter early, defaulting to `production` when omitted.
