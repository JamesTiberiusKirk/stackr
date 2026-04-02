# ADR 0002: Runtime State, Auth System, and API Expansion

## Status

Proposed

## Context

Stackr is evolving from a Docker Compose stack management tool into a self-hosted PaaS (Dokploy/Railway alternative) where **files remain the source of truth** for desired state. To support a future web frontend and remote CLI, several foundational capabilities are missing:

1. **No runtime state**: Deployment history, cron execution results, and system events are ephemeral — lost on daemon restart. A frontend has nothing to display.
2. **No authentication**: The only auth is a single Bearer token from an env var. This is insufficient for multi-user access, a web UI, or protecting deployed services.
3. **Minimal API surface**: Only `/deploy` and `/healthz` exist. A frontend and remote CLI need endpoints for stacks, deployments, cron, config, remote stacks, and user management.
4. **No forward auth**: Deployed services (Grafana, etc.) cannot be protected behind stackr's auth. Users currently need a separate tool like Authelia.

## Decision

### 1. SQLite State Store

Add an embedded SQLite database (`modernc.org/sqlite`, CGO-free) for runtime state. The database is **derived and disposable** — if deleted, the daemon re-derives current state from files and Docker on startup. Only historical data (deployment logs, cron execution history) is lost. User accounts are re-created from config (passwords need to be re-set via invite or CLI).

**Storage location**: `{repoRoot}/data/stackr.db` (configurable via `db_path` in `.stackr.yaml` or `STACKR_DB_PATH` env var).

**Schema** (6 tables):

- `users` — id, username, email, display_name, password_hash (Argon2id PHC format), role, active, totp_secret (nullable, for future 2FA), totp_enabled, created_at, updated_at
- `sessions` — id, user_id, token, expires_at, created_at
- `invite_tokens` — id, username, token, expires_at, used, created_at
- `deployments` — id, stack, tag, status (started/success/failed/rolled_back), trigger (api/cli/watcher), error, started_at, finished_at, stdout
- `cron_executions` — id, stack, service, schedule, status (started/success/failed), trigger (scheduled/manual/run_on_deploy), error, started_at, finished_at, container
- `events` — id, kind (stack.created/stack.removed/stack.archived/cron.reloaded), stack, message, created_at

**Package**: `internal/state/` with a `Store` interface and `SQLiteStore` implementation. The store is nil-safe — the CLI binary doesn't open a database, only the daemon does.

### 2. Auth System

Use **hamr** (`github.com/FyrmForge/hamr`) for auth primitives:
- `pkg/auth` — Argon2id password hashing, cookie-based session management
- `pkg/middleware` — Echo middleware for session auth, RBAC, active-user checks
- `pkg/ctx` — type-safe Echo context helpers

#### User Model

Users are declared in `.stackr.yaml` (public-safe, no secrets):

```yaml
auth:
  users:
    - username: dumitru
      email: dumitru@example.com
      display_name: Dumitru
      role: admin
    - username: dave
      email: dave@example.com
      display_name: Dave
      role: viewer
      stacks:
        grafana: ["stacks.deploy", "stacks.cron.run"]
```

Fields in config: username, email, display_name, role, per-stack permission overrides. No passwords, no secrets.

Fields in SQLite only: id, password_hash, active, totp_secret, totp_enabled, timestamps.

On daemon startup, users from config are synced to SQLite — missing users are created (without passwords), roles/email/display_name are updated, existing passwords and TOTP secrets are not touched.

#### No Registration

There is no user registration. This is an infrastructure control plane, not an end-user application. Users are created exclusively by:
1. Being declared in `.stackr.yaml`
2. Being invited via the API/CLI by someone with `auth.users.manage`

#### Password Management

Passwords are stored in SQLite (Argon2id via hamr) and set via:
- **Invite tokens** — admin generates a token, user accepts and sets their password
- **CLI command** — `stackr set-password <username>` (requires shell access)
- **`STACKR_ADMIN_PASSWORD` env var** — bootstrap the initial admin account

#### 2FA (Future)

TOTP support is designed into the user model (`totp_secret`, `totp_enabled` columns) but not implemented in the first pass. When implemented: after password verification, prompt for a 6-digit TOTP code before creating the session. Uses `github.com/pquerna/otp`.

#### Permission Model

Permissions are stack-centric. Everything that operates on a stack lives under `stacks.*`:

```
stacks.view                  # see stack list and status
stacks.deploy                # trigger deployments
stacks.create                # add new stacks
stacks.delete                # remove stacks
stacks.restart               # restart without full deploy
stacks.teardown              # stop containers
stacks.config.view           # see compose file, stackr/config.yaml
stacks.config.edit           # modify stack config
stacks.env.view              # see env vars for this stack
stacks.env.edit              # modify env vars
stacks.cron.view             # see cron jobs and execution history
stacks.cron.run              # manually trigger cron jobs
stacks.deployments.view      # see deployment history
stacks.remote.view           # see remote stack status
stacks.remote.sync           # trigger git sync
stacks.logs                  # view container logs

events.view                  # see event log

auth.users.view              # see user list
auth.users.manage            # create/edit/delete users, generate invites
auth.roles.manage            # create/edit roles and permissions

config.view                  # see global stackr config (sanitized)
config.env.view              # see global .env file
config.env.edit              # modify global .env

system.forward_auth          # access via forward auth (for protected stacks)
```

**Roles** are defined in `.stackr.yaml` as named permission bundles:

```yaml
auth:
  roles:
    admin:
      permissions: ["*"]
    operator:
      permissions: ["stacks.*", "events.view"]
    viewer:
      permissions: ["stacks.view", "stacks.deployments.view", "stacks.cron.view", "events.view"]
```

**Per-stack permissions** can be assigned in two places:

1. **Global config** (`/.stackr.yaml`) — infra team assigns stack-scoped overrides per user:
   ```yaml
   auth:
     users:
       - username: dave
         role: viewer
         stacks:
           grafana: ["stacks.deploy", "stacks.cron.run"]
   ```

2. **Stack-local config** (`stacks/myapp/stackr/config.yaml`) — dev teams manage their own access:
   ```yaml
   auth:
     users:
       - username: dave
         permissions: ["stacks.view", "stacks.deploy", "stacks.cron.run"]
   ```

Stack-local permissions can only grant stack-scoped permissions (`stacks.*`). System-level permissions (`auth.*`, `config.*`, `events.*`) can only be granted globally.

**Resolution** is additive (grant-only, never deny):
1. Global role permissions (applies everywhere)
2. Global per-stack overrides (scoped to named stacks)
3. Stack-local config users (scoped to that stack)
4. `*` (admin) overrides everything

#### Forward Auth

Traefik calls `GET /api/v1/auth/verify` to protect deployed services. The endpoint checks the session cookie, looks up the stack's access level from config, and returns 200 (allow) or 401 (redirect to login). This enables SSO across all services under the same base domain — server admins log in once via stackr and are authenticated for Grafana, monitoring dashboards, etc.

**Cookie domain** is inferred from the existing `http.base_domain` config (prefixed with `.` to cover subdomains). No separate `cookie_domain` config needed.

Access levels per stack are configured in:
- Global config: `auth.access.grafana: admin`
- Stack-local config: `auth.access: auth`

Values: `public` (no auth), `auth` (any logged-in user), or a role name (must have that role).

Traefik label on protected stacks:
```yaml
labels:
  traefik.http.middlewares.stackr-auth.forwardauth.address: "http://stackrd:9000/api/v1/auth/verify"
  traefik.http.middlewares.stackr-auth.forwardauth.authResponseHeaders: "X-Stackr-User"
```

### 3. Switch to Echo Router

Replace stdlib `http.ServeMux` with Echo (`github.com/labstack/echo/v4`). This is required by hamr's middleware and provides better routing, middleware chaining, and parameter extraction.

Legacy endpoints (`/deploy`, `/healthz`) are preserved for backward compatibility.

### 4. API Expansion

New endpoints under `/api/v1/` with session-based auth and permission checks:

**Auth (no session required):**

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/auth/login` | Username + password → session cookie |
| POST | `/api/v1/auth/logout` | Destroy session |
| POST | `/api/v1/auth/accept-invite` | Invite token + new password → set credentials |
| GET | `/api/v1/auth/verify` | Forward auth for Traefik |

**Auth (session required):**

| Method | Path | Permission | Description |
|--------|------|-----------|-------------|
| GET | `/api/v1/auth/me` | (any) | Current user info + permissions |

**Stacks:**

| Method | Path | Permission | Description |
|--------|------|-----------|-------------|
| GET | `/api/v1/stacks` | stacks.view | List stacks + live Docker status |
| GET | `/api/v1/stacks/{name}` | stacks.view | Stack detail (config, services, status) |
| POST | `/api/v1/stacks/{name}/deploy` | stacks.deploy | Deploy a stack |

**Deployments:**

| Method | Path | Permission | Description |
|--------|------|-----------|-------------|
| GET | `/api/v1/deployments` | stacks.deployments.view | List history (?stack=&limit=&offset=) |
| GET | `/api/v1/deployments/{id}` | stacks.deployments.view | Single deployment detail |

**Cron:**

| Method | Path | Permission | Description |
|--------|------|-----------|-------------|
| GET | `/api/v1/cron/jobs` | stacks.cron.view | List discovered jobs |
| POST | `/api/v1/cron/jobs/{stack}/{service}/run` | stacks.cron.run | Manual execution |
| GET | `/api/v1/cron/executions` | stacks.cron.view | Execution history |
| GET | `/api/v1/cron/executions/{id}` | stacks.cron.view | Single execution |

**Remote:**

| Method | Path | Permission | Description |
|--------|------|-----------|-------------|
| GET | `/api/v1/remote/stacks` | stacks.remote.view | List remote stacks |
| GET | `/api/v1/remote/stacks/{name}` | stacks.remote.view | Remote stack info |
| POST | `/api/v1/remote/stacks/{name}/sync` | stacks.remote.sync | Trigger git sync |

**Events + Config:**

| Method | Path | Permission | Description |
|--------|------|-----------|-------------|
| GET | `/api/v1/events` | events.view | Event log |
| GET | `/api/v1/config` | config.view | Sanitized global config |

**Users:**

| Method | Path | Permission | Description |
|--------|------|-----------|-------------|
| GET | `/api/v1/users` | auth.users.view | List users |
| POST | `/api/v1/users/{id}/invite` | auth.users.manage | Generate invite token |
| DELETE | `/api/v1/users/{id}` | auth.users.manage | Delete user |

Pagination: `?limit=N&offset=N`, default limit=50.

### 5. Web UI

Templ + HTMX + Alpine.js served by the same Echo instance under `/`. Same session cookie works for both API and web routes. Built incrementally starting with login, dashboard, and stack list.

### 6. Instrumentation

Existing operations (runner, cron scheduler, removal handler, file watcher) record events to the state store at key points — deployment start/success/failure, cron execution, stack creation/removal. The store field is nil-safe so the CLI binary is unaffected.

### 8. OIDC Provider (❓ UNDER DISCUSSION)

Stackr acts as an OpenID Connect provider, allowing deployed stacks and external services on any domain to authenticate users against stackr's user database. This eliminates the need for Authelia/Authentik and enables true SSO across all services, regardless of domain.

Uses `github.com/zitadel/oidc` for protocol compliance.

**Standard OIDC endpoints:**

| Path | Description |
|------|-------------|
| `/.well-known/openid-configuration` | Discovery document |
| `/oidc/authorize` | Authorization endpoint (redirects user to login) |
| `/oidc/token` | Token endpoint (exchanges code for tokens) |
| `/oidc/userinfo` | Userinfo endpoint (returns user profile) |
| `/oidc/jwks` | JSON Web Key Set (public signing keys) |

**Client registration** is declarative. A stack opts into OIDC via its config:

```yaml
# stacks/grafana/stackr/config.yaml
auth:
  access: admin
  oidc:
    enabled: true
    redirect_uri: https://grafana.stackr.company.dev/login/generic_oauth
```

On deploy, stackr automatically:
1. Generates a `client_id` + `client_secret` for the stack (stored in SQLite, per-stack)
2. Injects them as auto-provisioned env vars alongside existing `STACKR_PROV_*` vars:
   - `STACKR_OIDC_CLIENT_ID`
   - `STACKR_OIDC_CLIENT_SECRET`
   - `STACKR_OIDC_ISSUER_URL` (e.g. `https://stackr.company.dev`)
   - `STACKR_OIDC_AUTH_URL` (`{issuer}/oidc/authorize`)
   - `STACKR_OIDC_TOKEN_URL` (`{issuer}/oidc/token`)
   - `STACKR_OIDC_USERINFO_URL` (`{issuer}/oidc/userinfo`)
3. Registers the redirect_uri for the client

The stack's compose file references them directly:

```yaml
# stacks/grafana/docker-compose.yml
services:
  grafana:
    environment:
      GF_AUTH_GENERIC_OAUTH_CLIENT_ID: ${STACKR_OIDC_CLIENT_ID}
      GF_AUTH_GENERIC_OAUTH_CLIENT_SECRET: ${STACKR_OIDC_CLIENT_SECRET}
      GF_AUTH_GENERIC_OAUTH_AUTH_URL: ${STACKR_OIDC_AUTH_URL}
      GF_AUTH_GENERIC_OAUTH_TOKEN_URL: ${STACKR_OIDC_TOKEN_URL}
      GF_AUTH_GENERIC_OAUTH_API_URL: ${STACKR_OIDC_USERINFO_URL}
```

**Key design points:**
- Client credentials are per-stack, stored in SQLite, never in `.env` or git
- Credentials are auto-generated on first deploy and stable across subsequent deploys
- JWT signing key pair is generated on first daemon startup, stored in SQLite
- ID tokens include: `sub` (user id), `preferred_username`, `email`, `name` (display_name), and `roles` claim
- The `roles` claim carries the user's stackr role, allowing apps like Grafana to map roles to their own permission model
- Consent is implicit — all registered clients are trusted (this is an infrastructure control plane, not a public IdP)
- Works across any domain (unlike forward auth which requires same base domain)

**SQLite additions:**

```sql
CREATE TABLE oidc_clients (
    id            TEXT PRIMARY KEY,  -- client_id (UUID)
    stack         TEXT NOT NULL UNIQUE,
    secret_hash   TEXT NOT NULL,     -- hashed client_secret
    redirect_uris TEXT NOT NULL,     -- JSON array
    created_at    TEXT NOT NULL
);

CREATE TABLE oidc_auth_codes (
    code       TEXT PRIMARY KEY,
    client_id  TEXT NOT NULL,
    user_id    TEXT NOT NULL,
    scope      TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE oidc_signing_keys (
    id          TEXT PRIMARY KEY,
    private_key TEXT NOT NULL,       -- PEM-encoded RSA private key
    created_at  TEXT NOT NULL
);
```

**Forward auth vs OIDC:**
- Forward auth (cookie-based) covers same-domain services with zero app-side config
- OIDC covers cross-domain services and apps that natively support it (Grafana, Gitea, Portainer, etc.)
- Both use the same user database and permission model
- A stack can use either or both

### 9. Secret Management

The `.env` file remains the source of truth for secrets. This preserves the current backup model (repo + .env + volumes = complete system). Access to env vars is controlled at the API level via `stacks.env.view` / `stacks.env.edit` and `config.env.view` / `config.env.edit` permissions. No encryption at rest for now — the threat model for a home server doesn't justify the complexity.

## Consequences

### What becomes easier

- **Frontend development**: Full API surface with auth, permissions, and state to power a web UI
- **Multi-user access**: Teams can manage their own stacks with scoped permissions
- **Operational visibility**: Deployment history, cron execution logs, and system events are queryable
- **Service protection**: Forward auth provides SSO for same-domain services; OIDC provider enables SSO across any domain, replacing Authelia/Authentik entirely
- **Zero-config app auth**: Stacks opt into OIDC with a single config flag — client credentials are auto-provisioned and injected as env vars
- **Remote CLI**: Session-based auth works for CLI tools connecting to the daemon remotely
- **Debugging**: "What happened?" is answerable from the API without SSH access
- **Backup model preserved**: `.env` stays as-is, SQLite is disposable (users recreated from config)

### What becomes harder

- **Dependency footprint**: Adding Echo, hamr, SQLite, and Templ increases binary size and build complexity
- **Config complexity**: Auth section in `.stackr.yaml` is a significant addition to learn and configure
- **Permission debugging**: Three sources of permissions (global role, global per-stack, stack-local) can be confusing. Mitigated by keeping resolution strictly additive.
- **Database management**: SQLite file needs to be on a persistent volume in Docker deployments
- **hamr coupling**: Depending on a framework still in active development. Mitigated by only using stable, well-scoped packages (auth, middleware, ctx).

## Alternatives Considered

### State store alternatives

- **JSON files**: Simpler but painful to query (e.g., "last 10 deploys for stack X"). No indexing.
- **bbolt**: KV store, forces manual serialization and indexing. SQLite gives relational queries for free.
- **PostgreSQL**: More capable but requires external infrastructure. SQLite is zero-dependency. Postgres can be added as an option later.

### Auth alternatives

- **Keep single Bearer token**: Insufficient for multi-user, web UI, and forward auth.
- **API keys per user**: Simple but no session management for a web frontend.
- **User registration**: Considered and rejected — this is an infrastructure control plane, not an end-user application. Every user should be intentionally created by an admin.
- **Delegate to Authelia/Authentik**: Defeats the purpose of stackr as a self-contained control plane.
- **Forward auth only (no OIDC)**: Covers same-domain services but not cross-domain. Many apps (Grafana, Gitea, Portainer) natively support OIDC, making it the more robust long-term solution. Forward auth is still useful as a simpler option for apps that don't support OIDC.

### Secret management alternatives

- **Secrets in SQLite**: Would break the backup model (repo + .env + volumes). Mixes secrets with deployment history.
- **Encrypted `.env`**: Requires managing an encryption key, which is just another secret. Over-engineered for the threat model.
- **External secret store (Vault, Doppler)**: Adds infrastructure dependencies. Can be integrated later if needed.

### Router alternatives

- **Keep stdlib ServeMux**: Go 1.22+ supports method routing, but hamr's middleware requires Echo. Mixing two routers adds complexity.
- **chi/gorilla/fiber**: All viable, but hamr is built on Echo. Using Echo avoids adapter layers.

### Frontend alternatives

- **Separate SPA (React/Vue/Svelte)**: More flexible UI but adds a build pipeline, CORS handling, and a second deployment artifact. Contradicts the single-binary goal.
- **No frontend (API only)**: Viable short-term but the web UI is a core part of the PaaS vision.

## Future Directions

The following are acknowledged as future work. The current design should not prevent these but does not implement them:

- **Multi-server / multi-node**: Hub-and-spoke model where one stackrd acts as control plane, agents run on other servers. Declarative placement via server labels, dedicated builder nodes. See [ADR-0004](0004-multi-node.md).
- **Multi-environment per stack**: Production, staging, PR environments from a single stack definition. Ephemeral PR environments with auto-teardown. See [ADR-0003](0003-multi-environment-stacks.md).
- **OIDC provider**: Stackr as a full OpenID Connect identity provider for cross-domain SSO and native app auth integration. Auto-provisioned client credentials per stack.

## Implementation Order

1. State store package (`internal/state/`)
2. Config changes (auth section, db_path)
3. Add hamr + Echo dependencies
4. Rewrite daemon to Echo, wire store, user sync from config
5. Auth endpoints (login, invite, verify, me)
6. Instrument runner, scheduler, removal handler
7. Docker status helper (`internal/docker/status.go`)
8. Full API endpoints with permission checks
9. Web UI (incremental — login, dashboard, stack list, cron pages)
10. Forward auth integration
11. OIDC provider (discovery, authorize, token, userinfo, JWKS endpoints)
12. OIDC client auto-provisioning (per-stack client_id/secret, env var injection on deploy)
13. CLI auth commands (`set-password`, `invite`)
14. Tests and dev environment updates
