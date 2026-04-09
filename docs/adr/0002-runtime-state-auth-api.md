# ADR 0002: Runtime State, Auth, and HTTP Surface

## Status

Proposed

## Context

Stackr is evolving from a Docker Compose stack manager into a self-hosted PaaS (Dokploy/Railway alternative) where **files remain the source of truth** for desired state. To support a future web frontend, remote CLI, and multi-user access, four foundational capabilities are missing:

1. **No runtime state** — deployment history, cron execution results, and system events are ephemeral, lost on daemon restart. A frontend has nothing to display.
2. **No authentication** — the only auth is a single Bearer token from an env var. Insufficient for multi-user, web UI, or protecting deployed services.
3. **Minimal API surface** — only `/deploy` and `/healthz`. A frontend and remote CLI need endpoints for stacks, deployments, cron, config, remote stacks, and user management.
4. **No forward auth** — deployed services (Grafana, etc.) cannot be protected behind stackr's auth. Users currently need a separate tool like Authelia.

## Decision

### 1. SQLite state store

Embed SQLite (`modernc.org/sqlite`, CGO-free) for runtime state. The database is **derived and disposable** — if deleted, the daemon re-derives current state from files and Docker on startup. Only historical data (deployment logs, cron history, events) is lost. User accounts are re-synced from config on startup; passwords must be re-set via invite or CLI.

Storage at `{repoRoot}/data/stackr.db`, overridable via `db_path` in `.stackr.yaml` or `STACKR_DB_PATH`.

Initial schema (6 tables):

- `users` — id, username, email, display_name, password_hash (Argon2id PHC), role, active, totp_secret, totp_enabled, timestamps
- `sessions` — id, user_id, token, expires_at, created_at
- `invite_tokens` — id, username, token, expires_at, used, created_at
- `deployments` — id, stack, tag, status, trigger, error, started_at, finished_at, stdout
- `cron_executions` — id, stack, service, schedule, status, trigger, error, started_at, finished_at, container
- `events` — id, kind, stack, message, created_at

Lives in `internal/state/` with a `Store` interface and `SQLiteStore` implementation. The store is nil-safe — the CLI binary never opens a database, only the daemon does.

### 2. Adopt hamr as HTTP/auth/templating stack

Replace stdlib `http.ServeMux` and the roll-your-own auth story with **hamr** (`github.com/FyrmForge/hamr`), a full-stack Go framework bundling Echo v4, Templ, HTMX, Alpine.js, Argon2id, cookie sessions, RBAC middleware, and content-negotiation helpers. Legacy `/deploy` and `/healthz` are preserved.

**Database coupling is confined to hamr's `pkg/db` (Postgres-only via pgx), which we skip entirely.** The auth, session, middleware, server, respond, htmx, and ctx packages are DB-agnostic — they take interfaces (`SessionStore`, `SubjectLoader`, callback functions). Stackr provides a SQLite-backed `SessionStore` implementation and a `SubjectLoader` that queries the `users` table. Migrations are embedded SQL files executed by `internal/state` at open time, not via hamr's Postgres-specific migration helpers.

Stackr also keeps its own `internal/config` (YAML via yaml.v3) and does **not** adopt hamr's env-var-only `pkg/config`. Stackr's existing logger (stdlib `log`) stays for now; a per-binary `slog` migration (CLI text handler, daemon JSON handler) is deferred to its own follow-up.

#### User model

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
      role: viewer
      stacks:
        grafana: ["stacks.deploy", "stacks.cron.run"]
```

Config fields: `username`, `email`, `display_name`, `role`, per-stack permission overrides. No passwords, no secrets.

SQLite-only fields: `id`, `password_hash`, `active`, `totp_secret`, `totp_enabled`, timestamps.

On daemon startup, users from config are synced to SQLite — missing users are created, roles/email/display_name are updated, existing passwords and TOTP secrets are preserved.

There is **no user registration**. This is an infrastructure control plane, not an end-user app. Users are created exclusively by declaration in `.stackr.yaml` or invite issued by someone with `auth.users.manage`.

#### Password management

Argon2id via hamr's `pkg/auth`. Passwords are set via:

- **Invite tokens** — admin generates a token, user accepts and sets their password.
- **CLI** — `stackr set-password <username>` (requires shell access).
- **`STACKR_ADMIN_PASSWORD` env var** — bootstrap the initial admin account.

TOTP 2FA is designed into the user model (`totp_secret`, `totp_enabled` columns) but not implemented in the first pass.

#### Permission model

Permissions are stack-centric. Stack-scoped names live under `stacks.*` (e.g. `stacks.view`, `stacks.deploy`, `stacks.cron.run`, `stacks.deployments.view`, `stacks.env.edit`, `stacks.remote.sync`, `stacks.logs`). System-scoped names live under `auth.*` (user/role management), `config.*` (global config and env), and `events.*` (event log). The full list is defined in code alongside the middleware that enforces it; it is not fixed in this ADR because it will grow with the API surface.

**Roles** are named permission bundles defined in `.stackr.yaml`:

```yaml
auth:
  roles:
    admin:
      permissions: ["*"]
    operator:
      permissions: ["stacks.*", "events.view"]
    viewer:
      permissions: ["stacks.view", "stacks.deployments.view", "events.view"]
```

**Per-stack overrides** can be granted in two places:

1. **Global config** (`.stackr.yaml`, `auth.users[].stacks`) — infra team assigns stack-scoped permissions per user.
2. **Stack-local config** (`stacks/myapp/stackr/config.yaml`, `auth.users`) — dev teams manage their own access, but only for stack-scoped permissions (`stacks.*`). System-level permissions can only be granted globally.

Resolution is **additive** (grant-only, never deny): global role → global per-stack → stack-local → `*` wildcard wins. Enforced as a `RoleChecker` callback passed to hamr's RBAC middleware.

#### Forward auth

Traefik calls `GET /api/v1/auth/verify` with the session cookie. The endpoint checks the session, resolves the stack's required access level from config, and returns 200 (allow) or 401 (redirect to login). This enables SSO across all services under the same base domain.

Cookie domain is inferred from the existing `http.base_domain` config (prefixed with `.` for subdomains).

Access level per stack is configured globally (`auth.access.grafana: admin`) or stack-locally (`auth.access: auth`). Values: `public`, `auth` (any logged-in user), or a role name.

Stacks opt into forward auth via the `stackr.traefik.auth` shorthand label — see Section 4.

### 3. HTTP surface

New endpoints under `/api/v1/` with session auth and permission checks. **API and Web UI ship from the same handlers** via hamr's `pkg/respond` content negotiation: a single handler returns a Templ component for HTMX/browser requests and JSON for API clients, chosen by the `Accept` / `HX-Request` headers. There is no separate "Web UI codebase" — the HTML experience is built incrementally alongside each endpoint as it lands.

**Auth (no session required):**

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/auth/login` | Username + password → session cookie |
| POST | `/api/v1/auth/logout` | Destroy session |
| POST | `/api/v1/auth/accept-invite` | Invite token + new password |
| GET | `/api/v1/auth/verify` | Forward auth for Traefik |

**Session required** (all also serve HTML via content negotiation):

| Method | Path | Permission | Description |
|--------|------|-----------|-------------|
| GET | `/api/v1/auth/me` | — | Current user + permissions |
| GET | `/api/v1/stacks` | `stacks.view` | List stacks + live Docker status |
| GET | `/api/v1/stacks/{name}` | `stacks.view` | Stack detail |
| POST | `/api/v1/stacks/{name}/deploy` | `stacks.deploy` | Deploy |
| GET | `/api/v1/deployments` | `stacks.deployments.view` | History (`?stack=&limit=&offset=`) |
| GET | `/api/v1/deployments/{id}` | `stacks.deployments.view` | Single deployment |
| GET | `/api/v1/cron/jobs` | `stacks.cron.view` | List discovered jobs |
| POST | `/api/v1/cron/jobs/{stack}/{service}/run` | `stacks.cron.run` | Manual execution |
| GET | `/api/v1/cron/executions` | `stacks.cron.view` | Execution history |
| GET | `/api/v1/remote/stacks` | `stacks.remote.view` | List remote stacks |
| POST | `/api/v1/remote/stacks/{name}/sync` | `stacks.remote.sync` | Trigger git sync |
| GET | `/api/v1/events` | `events.view` | Event log |
| GET | `/api/v1/config` | `config.view` | Sanitized global config |
| GET | `/api/v1/users` | `auth.users.view` | List users |
| POST | `/api/v1/users/{id}/invite` | `auth.users.manage` | Generate invite |
| DELETE | `/api/v1/users/{id}` | `auth.users.manage` | Delete user |

Pagination: `?limit=N&offset=N`, default 50.

Existing operations (runner, cron scheduler, removal handler, file watcher) write deployment/cron/event rows to the state store at key points. The store field is nil-safe so the CLI binary is unaffected.

### 4. Traefik label shorthand

Wiring a service through Traefik via raw labels takes 8–10 lines of boilerplate per service (router rule, entrypoints, TLS, certresolver, port, network, middleware refs). For auth-protected stacks, users also maintain a growing list of middleware definitions in Traefik's file provider (one per auth variant — `stackr-auth`, `stackr-auth-admin`, etc.). This is copy-paste prone.

Stackr introduces a shorthand label namespace (`stackr.traefik.*`) that expands into full Traefik labels at deploy time via a generated Compose override file. The user's Compose files are never modified — the override is passed alongside via `docker compose -f compose.yml -f compose.stackr-traefik.yml`.

**Shorthand labels:**

```yaml
labels:
  - stackr.traefik.port=8080                  # required
  - stackr.traefik.subdomain=api              # optional, default = stack name
  - stackr.traefik.entrypoints=websecure      # optional
  - stackr.traefik.middleware=ratelimit@file  # optional extra middleware
  - stackr.traefik.auth=required              # optional, enable forward auth
  - stackr.traefik.auth.role=admin            # optional, restrict by role
```

**What stackr generates** in the override: `traefik.enable=true`, docker network attachment, router rule from `STACKR_PROV_DOMAIN` (or subdomain override), entrypoints + TLS + certresolver from defaults, service loadbalancer port, middleware chain (user-specified + `stackr-auth@file` when `auth=required`). Router names are derived from stack + service to prevent collisions.

**Defaults** match what `stackr add traefik` scaffolds: network `traefik`, entrypoint `websecure`, certresolver `letsencrypt`, forward auth middleware `stackr-auth`. BYO-Traefik users either rename their resources to match or override per service via labels. No global `traefik:` block in `.stackr.yaml` for now — can be added later purely additively.

**Role-based forward auth**: `stackr.traefik.auth.role=admin` cannot share a single `stackr-auth@file` middleware — each role needs its own middleware pointing at `/api/v1/auth/verify?role=admin`. Stackr generates these as labels on the service itself rather than requiring users to maintain one middleware per role in Traefik's file provider. Auth wiring stays co-located with the service, file provider config does not grow over time.

**`stackr add traefik` scaffold** generates a Traefik stack under `stacks/traefik/` with Docker + file provider, Let's Encrypt certresolver, the base `stackr-auth` middleware, external network, and a README. Opt-in; BYO-Traefik users skip it.

**What stackr does NOT do**: own Traefik's lifecycle, write to Traefik's dynamic config directory after scaffolding, or re-expose Traefik's full label API. Advanced users write raw Traefik labels for TCP routing, weighted load balancing, custom middleware chains — both styles coexist on the same service.

### 5. OIDC provider (future — under discussion)

Stackr as an OpenID Connect provider, allowing deployed stacks and external services on any domain to authenticate users against stackr's user database. Eliminates Authelia/Authentik and enables SSO across all services regardless of domain. Uses `github.com/zitadel/oidc` for protocol compliance.

Client registration is declarative — a stack opts in via `auth.oidc.enabled: true` in its config. On deploy, stackr auto-generates a `client_id` + `client_secret` per stack, stores them in SQLite (never in `.env` or git), and injects them as `STACKR_OIDC_*` env vars alongside the existing `STACKR_PROV_*` vars. The stack's compose file references them directly (`${STACKR_OIDC_CLIENT_ID}`, etc.).

Key design points: per-stack credentials stored only in SQLite; JWT signing keys generated once on first daemon start; ID tokens carry a `roles` claim for apps like Grafana to map into their own permission model; works across any domain (unlike forward auth). Hamr's session cookie is the user-facing auth layer — when a request hits `/oidc/authorize`, hamr middleware handles login, then stackr mints an OIDC code. zitadel/oidc's `op.Storage` interface is a separate concern from hamr's `SessionStore` — both are backed by our SQLite.

Concrete endpoint list, storage schema, and implementation details are deferred to the phase that actually builds this.

### 6. Secret management

The `.env` file remains the source of truth for secrets. Preserves the current backup model (repo + `.env` + volumes = complete system). Access to env vars is controlled at the API level via `stacks.env.view` / `stacks.env.edit` and `config.env.view` / `config.env.edit` permissions. No encryption at rest for now — the threat model for a home server doesn't justify the complexity.

## Consequences

### What becomes easier

- **Frontend is free.** Hamr's content negotiation means every API handler also serves HTML. No separate frontend codebase.
- **Multi-user access.** Teams manage their own stacks with scoped permissions.
- **Operational visibility.** Deployment history, cron execution logs, and events are queryable.
- **Service protection.** Forward auth provides SSO for same-domain services; future OIDC covers cross-domain.
- **Zero-config routing.** Stacks opt into Traefik and forward auth with one label.
- **Remote CLI.** Session-based auth works for CLI tools connecting to the daemon remotely.
- **Backup model preserved.** `.env` unchanged, SQLite is disposable.

### What becomes harder

- **Dependency footprint.** Hamr brings Echo, Templ, HTMX assets, Argon2id, and its middleware stack. Binary size grows.
- **Config complexity.** The `auth:` section in `.stackr.yaml` is a significant addition to learn and configure.
- **Permission debugging.** Three sources (global role, global per-stack, stack-local) can be confusing. Mitigated by strictly additive resolution.
- **Database management.** The SQLite file needs a persistent volume in Docker deployments.
- **hamr coupling.** We depend on a framework still in active development. Mitigated by skipping `pkg/db` (the only Postgres-coupled package) and consuming hamr only as stable library packages — not via its scaffolding CLI.
- **Traefik opinion.** The shorthand ties simplified routing UX to Traefik specifically. Caddy/Nginx users can still use stackr but don't benefit from the shorthand and must wire forward auth manually.

## Alternatives considered

### State store

- **JSON files** — simpler but painful to query ("last 10 deploys for stack X"). No indexing.
- **bbolt** — KV store, forces manual serialization and indexing. SQLite gives relational queries for free.
- **Postgres** — more capable but requires external infra. SQLite is zero-dep. Postgres can be added as an alternative later.

### HTTP stack

- **Stdlib `ServeMux` + custom auth** — Go 1.22+ supports method routing, but rolling session management, RBAC, CSRF, and templating from scratch is exactly the wheel hamr has already built.
- **chi/gorilla/fiber + hand-rolled auth** — all viable routers, but we'd still write the auth and templating layers. Hamr wraps Echo and gives us the whole stack.
- **hamr as a DB-backed framework (use `pkg/db`)** — would require switching stackr to Postgres, breaking the single-binary / disposable-state story. Rejected; hamr's auth/middleware layer is DB-agnostic, so we keep SQLite and skip `pkg/db` only.
- **Separate SPA (React/Vue/Svelte)** — more flexible UI but adds a build pipeline, CORS, and a second deployment artifact. Hamr's Templ + HTMX approach gives us incremental web UI from the same binary.

### Auth

- **Keep single Bearer token** — insufficient for multi-user, web UI, and forward auth.
- **API keys per user** — simple but no session management for a browser UI.
- **User registration** — rejected; infrastructure control plane, not a public app.
- **Delegate to Authelia/Authentik** — defeats the purpose of stackr as a self-contained control plane.
- **Forward auth only (no OIDC later)** — covers same-domain services but not cross-domain. Many apps (Grafana, Gitea, Portainer) natively support OIDC, making it the more robust long-term option. Forward auth is kept as the simpler same-domain fallback.

### Secret management

- **Secrets in SQLite** — would break the backup model and mix secrets with deployment history.
- **Encrypted `.env`** — requires managing an encryption key, which is just another secret. Over-engineered.
- **External secret store (Vault, Doppler)** — adds infrastructure dependencies. Can be integrated later if needed.

### Traefik integration

- **Convention only (docs + `STACKR_PROV_DOMAIN`)** — already supported today. Fine for routing, but forces every user to manage forward auth middleware in Traefik's file provider, which proliferates as roles are added.
- **Full Traefik lifecycle management** — stackr owns Traefik and writes its dynamic config directly. Rejected — couples stackr too tightly to Traefik and bypasses Compose-native philosophy.
- **Higher-level `routes:` block in `.stackr.yaml`** — rejected; moves routing config away from the service it belongs to, creates a second place to look for "why is this exposed", and re-exposes most of Traefik's label API through stackr's own config format.
- **Global `traefik:` block in `.stackr.yaml`** — deferred; hard-coded defaults matching the scaffold cover the common case, per-service overrides handle BYO-Traefik, and a global block can be added later purely additively.

## Implementation phases

Execution-level plans for each phase live in `docs/impl/`. The phases are:

- **Phase 0 — Plumbing.** `internal/state` package (SQLite store, schema, embedded migrations) + `internal/httpapi` rewrite on top of hamr (`pkg/server`, `pkg/auth`, `pkg/middleware`, `pkg/respond`, `pkg/htmx`, `pkg/ctx`). Legacy `/deploy` + `/healthz` preserved and still protected by the existing `STACKR_TOKEN` Bearer env var. No new user-visible features. See [phase-0-hamr-adoption.md](../impl/phase-0-hamr-adoption.md).
- **Phase 1 — Auth core.** Config extension for `auth.users` / `auth.roles`, user sync on daemon startup, `SessionStore` + `SubjectLoader` wired to real tables, login/logout/me/accept-invite handlers (HTML + JSON from the same handler), `STACKR_ADMIN_PASSWORD` bootstrap, `stackr set-password` CLI. Ends with: you can log into stackr.
- **Phase 2 — Read slice.** Instrument runner, cron scheduler, removal handler, and watcher to write to the state store. Ship read endpoints and their HTMX pages for stacks, deployments, cron executions, events, remote stacks. Ends with: "what happened?" is answerable from the browser or CLI.
- **Phase 3 — Write slice.** Deploy, manual cron run, remote sync, user management endpoints and pages. CLI switches from Bearer token to session auth. Ends with: CLI and browser can drive the daemon end-to-end.
- **Phase 4 — Forward auth + Traefik shorthand.** `/auth/verify`, label expander with Compose override generator, `stackr add traefik` scaffold, per-role auth middleware generation. Ends with: protected stacks work end-to-end from a single label.
- **Phase 5 — OIDC provider** *(if still desired)*. zitadel/oidc integration, `op.Storage` implementation against SQLite, per-stack client auto-provisioning with `STACKR_OIDC_*` env var injection.

Cross-cutting follow-ups with no specific phase: per-binary slog migration (CLI text handler, daemon JSON handler), TOTP 2FA, Postgres as an alternative state store.

## Future directions

The current design should not prevent these, but does not implement them:

- **Multi-server / multi-node** — hub-and-spoke model with agents. See [ADR-0004](0004-multi-node.md).
- **Multi-environment per stack** — production, staging, and ephemeral PR environments from one stack definition. See [ADR-0003](0003-multi-environment-stacks.md).
- **OIDC provider** — see Section 5; currently under discussion.
