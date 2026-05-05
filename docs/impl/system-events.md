# System events log + viewer

Status: planned, not started. Picked up after async actions / docker-events
watcher / file-reconcile already shipped.

## Goal

Centralised audit log of "everything the system does" — deploys, cron runs,
reconciles, env edits, auth actions, user syncs. Operators answer "did X
happen, when, and why" without grepping daemon logs.

## Existing

- `events` table in SQLite (already auto-migrated via
  `internal/db/models.go`). Columns: id, kind, stack, message, created_at.
- `EventKind*` constants declared in `internal/repo/event.go` but mostly
  unused.
- One writer today: `service.SetEnv` / `DeleteEnv` → `kind=env.edit`.
- Reconciles, deploys, cron, auth — none write events.
- No `/events` page anywhere.

## Plan

### 1. Event writes at the action sites

| Site | Kinds |
|------|-------|
| `service.PerformAction` (Up/Down/Restart/Update) | `stack.action.started` → `stack.action.succeeded` \| `stack.action.failed` |
| `reconcile.Apply` | `reconcile.applied` (one per `StackChange`, message = `"<reason> → <action>, job=<id>"`) |
| `cron.Recorder.Start` / `Finish` | `cron.started` → `cron.succeeded` \| `cron.failed` |
| `service.SetEnv` / `DeleteEnv` | `env.edit` (already lands — keep) |
| Login / logout / invite handlers | `auth.login.succeeded`, `auth.login.failed`, `auth.logout`, `auth.invite.issued`, `auth.invite.redeemed` |
| Boot-time user sync | `user.sync.applied` — only when plan non-empty |

Schema unchanged. Actor / severity encoded in `kind` (suffix `.failed`
vs `.succeeded`) and `message` text.

### 2. `/events` page

- New top-level nav link.
- Columns: timestamp, kind, stack, message.
- Filters: `?kind=…&stack=…`.
- Default page size 100; no pagination yet.
- Same `data-table` styling.

### 3. Skips for v1

- Severity column.
- Per-event detail page.
- Live updates (banner handles in-flight; events page is retrospective).
- Structured Plan storage (separate `reconcile_plans` table comes with
  the future approval-mode wedge — see project memory on plan/apply).

## Touches

- `internal/service/actions.go` — emit events around `PerformAction`.
- `internal/cron/recorder.go` — emit events alongside the existing row
  writes.
- `internal/reconcile/reconcile.go` — `Apply` needs a `Store` reference
  to call `CreateEvent`. Either inject a narrow `EventWriter` interface
  or extend the existing `ActionEnqueuer` closure to carry an event
  callback. Lean toward the interface — keeps Apply self-contained.
- `internal/web/handler/events/` — new package with handler + templ.
- `internal/web/server.go` — register `/events` route.
- `internal/web/components/layout.templ` — add nav link.
- Login / logout / invite handlers in `internal/web/handler/auth/`.
- `cmd/stackrd/main.go` — wire user-sync event emission.

## Open questions to settle when picking back up

- Is `auth.login.failed` worth keeping, or does it just invite log
  pollution from bots? Lean keep — it's the only signal of brute-force
  attempts today, and we can filter on `kind` later.
- Should `/events` accept a time-range filter on first cut, or is
  `?kind=…&stack=…` plus reverse-chronological order enough? Lean
  enough-for-v1.
- Where do reconcile events sit on the per-stack detail page — same
  panel as deployments, or a separate "Activity" panel? Probably the
  latter once we have it.

## Decisions already locked

- Use existing `events` table; no schema change for v1.
- Encode actor / severity in `kind` and `message`, not new columns.
- `reconcile.Apply` writes one event per `StackChange`, not one per
  Plan — easier to filter by stack.
- `auth.users.manage` permission not enforced yet (RBAC is step #4 of
  ADR-0002); for now the page is just `auth.RequireAuth()`.
