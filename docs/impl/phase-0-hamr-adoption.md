# Phase 0 — hamr adoption and state store

Execution plan for the first phase of [ADR-0002](../adr/0002-runtime-state-auth-api.md). This phase is pure plumbing — no new user-visible features, no behavior changes. The goal is to land the foundational infrastructure (SQLite state store + hamr HTTP stack) in a shape phase 1 can build on, without breaking existing `/deploy` or `/healthz` behavior.

## Goals

- `internal/state/` package compiles, has unit tests, exposes a `Store` interface and `SQLiteStore` implementation, and runs embedded migrations on `Open()`.
- `internal/httpapi/` is rewritten on top of hamr's `pkg/server`, `pkg/auth`, `pkg/middleware`, `pkg/respond`, `pkg/ctx`, and `pkg/htmx`.
- `/deploy` and `/healthz` continue to work, still protected by the current `STACKR_TOKEN` Bearer env var. No session auth yet.
- `cmd/stackrd/main.go` constructs the server via `server.New(...)` instead of stdlib `http.ServeMux`.
- `cmd/stackr/main.go` and every non-HTTP internal package (`runner`, `compose`, `cronjobs`, `remote`, `removal`, `stackcmd`, `watch`) are **untouched**.
- `go run ./cmd/stackrd` boots against an existing test stack and deploys successfully.

## Non-goals (explicitly deferred)

- No login page, no user table populated, no sessions actually issued. `SessionStore` is implemented (trivial, satisfies hamr's interface) but no handler calls `CreateSession` yet.
- No real `SubjectLoader` — a stub returning `nil, nil` is fine until phase 1.
- No new endpoints. Only the two existing ones.
- No `stdlib log` → `slog` migration across shared packages. `internal/httpapi` uses slog via hamr; everything else stays on stdlib `log`. Mixed log output is accepted for phase 0.
- No config schema extension beyond `db_path`. The `auth:` section arrives in phase 1.
- No CLI changes. `cmd/stackr` is untouched.
- No templ pages, no static asset serving (HTMX/Alpine). First templ page ships in phase 1 (login).
- No TOTP, no OIDC, no Traefik shorthand, no forward auth.

## New files

- `internal/state/store.go` — `Store` interface (methods grow with each phase; phase 0 only needs the SessionStore surface + `Open`/`Close`/`Ping`).
- `internal/state/sqlite.go` — `SQLiteStore` implementation using `modernc.org/sqlite`.
- `internal/state/migrate.go` — embedded migration runner.
- `internal/state/migrations/0001_init.up.sql` — initial schema for all 6 tables (users, sessions, invite_tokens, deployments, cron_executions, events). We ship the full schema in phase 0 even though only `sessions` is exercised; landing the schema once is cheaper than fragmenting it across phases.
- `internal/state/migrations/0001_init.down.sql`
- `internal/state/session_store.go` — implementation satisfying `hamr/pkg/auth.SessionStore` (Create, GetByToken, Delete, DeleteBySubjectID).
- `internal/state/store_test.go` — unit tests against a temp sqlite file. No testcontainers needed; modernc is pure Go.

## Rewritten files

`internal/httpapi/handler.go` is split into three files:

- `internal/httpapi/server.go` — `New(cfg, store) (*Server, error)` constructor that builds a hamr `*server.Server`, registers middleware (secure headers, request logging; CSRF and CORS deferred until we actually have web routes in phase 1), wires the `SessionManager` against our `SessionStore`, and sets up a stub `SubjectLoader`.
- `internal/httpapi/routes.go` — route registration. `/healthz` and `/deploy` mounted as Echo handlers with the existing Bearer-token middleware preserved.
- `internal/httpapi/legacy.go` — the two legacy handlers rewritten with `func(c echo.Context) error` signatures. Business logic lifted verbatim from the current `handler.go`.

Existing tests:

- `internal/httpapi/handler_test.go` → renamed and updated for the new constructor. Assertions about HTTP behavior stay identical.
- `internal/httpapi/handler_integration_test.go` → updated to use the new constructor; behavior identical.

## Changed files

- `cmd/stackrd/main.go` — replace the `http.ListenAndServe(...)` block with `state.Open(cfg.DBPath)` → `httpapi.New(cfg, store)` → `srv.Start()`. Also wire graceful shutdown (hamr's `Start()` handles SIGINT/SIGTERM but we still need to close the store on the way out).
- `internal/config/config.go` — add `DBPath string` field, default `{repoRoot}/data/stackr.db`, env override `STACKR_DB_PATH`. One-line change to the YAML unmarshal struct plus default-fill logic.
- `go.mod` / `go.sum` — add:
  - `github.com/FyrmForge/hamr` (pin to a specific tag; note the version in the PR description)
  - `github.com/labstack/echo/v4` (transitive via hamr but explicit to lock version)
  - `modernc.org/sqlite`
  - Run `go mod tidy`.

Templ and HTMX assets are **not** added in phase 0 — nothing renders HTML yet.

## Migrations in phase 0

No external migration tool. `internal/state/migrate.go`'s runner:

1. Opens the sqlite file via `sql.Open("sqlite", dsn)`.
2. Creates a `schema_migrations (version INTEGER PRIMARY KEY)` table if missing.
3. Reads current version.
4. Iterates embedded `*.up.sql` files in numeric order, executing each whose version is greater than current, wrapped in a transaction.
5. Updates `schema_migrations` after each successful file.

Down-migrations exist in the tree for future use but are not executed automatically. Phase 0 only ships one migration (`0001_init`). We use the same machinery as the schema grows in later phases.

## Success criteria

- `make build` succeeds.
- `make test` passes — all existing tests, plus the new `internal/state` tests.
- `go run ./cmd/stackrd` boots, opens `data/stackr.db`, runs migrations, logs "server listening", and `curl localhost:9000/healthz` returns 200.
- `curl -X POST -H "Authorization: Bearer $STACKR_TOKEN" localhost:9000/deploy -d '{"stack":"..."}'` deploys a test stack identically to pre-phase-0 behavior.
- Deleting `data/stackr.db` and restarting the daemon re-creates it cleanly (the disposable-store property holds).
- `go run ./cmd/stackr list` (CLI) works unchanged.

## Risks and mitigations

- **hamr version churn.** Hamr is in active development. Pin to a specific tag in `go.mod`. If a breaking change lands upstream during phase 0, don't upgrade mid-phase.
- **modernc.org/sqlite under Alpine/musl.** modernc is pure Go (no CGO), but verify the Dockerfile base image still works; no extra `apk add` needed.
- **Integration tests that exec the daemon binary.** Any test that spawns `stackrd` needs `STACKR_DB_PATH` pointing at a temp file. Grep before landing.
- **Preserving `/deploy` behavior byte-for-byte.** The legacy handler's request/response contract must not shift — existing CI scripts and curl invocations from docs must still work. Integration tests should catch regressions.

## Sequencing within phase 0

Recommended PR order (each PR independently landable and leaves the daemon buildable):

1. **`internal/state` package, in isolation.** No httpapi changes. Code-reviewable by itself. Has unit tests. Nothing imports it from the daemon yet.
2. **`cmd/stackrd` opens the store but still uses stdlib mux.** Wires `state.Open()` at startup and passes the store into `httpapi.New()` (which currently ignores it). Proves store lifecycle works under the real daemon and surfaces any migration/boot issues before the HTTP rewrite.
3. **`internal/httpapi` rewrite to hamr.** The big PR. `pkg/server`, middleware chain, legacy route port, test updates. `cmd/stackrd/main.go` switches to the new constructor.

Phase 0 is done when PR 3 merges green.
