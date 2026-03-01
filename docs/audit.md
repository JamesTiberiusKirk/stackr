# Stackr Repository Audit

## Overview

Stackr is a declarative Docker Compose stack deployment system written in Go 1.25.3. It has two binaries:

- **`stackr`** (CLI) — local operations: init, update, tear-down, backup, compose, vars-only, get-vars, run-cron
- **`stackrd`** (daemon) — HTTP API server with file watching, cron scheduling, and automated deployments

Module path: `github.com/jamestiberiuskirk/stackr` | License: MIT | ~4,229 lines of Go across 22 files with ~40 tests.

---

## Critical Issues

### 1. HIGH: Path Traversal via Stack Names

**File:** `internal/httpapi/handler.go:223-239`

Stack names from HTTP requests are passed directly to `filepath.Join()` without sanitization. An attacker with a valid token could send `{"stack": "../../etc"}` and potentially access files outside the stacks directory.

```go
func (h *Handler) ensureStackExists(name string) error {
    stackDir := filepath.Join(h.cfg.StacksDir, name)  // 'name' NOT validated
    composePath := filepath.Join(stackDir, "docker-compose.yml")
    // ...
}
```

Same pattern in `internal/cronjobs/scheduler.go:265`. Needs input validation rejecting `/`, `..`, and null bytes.

---

### 2. MEDIUM: Timing-Unsafe Token Comparison

**File:** `internal/httpapi/handler.go:201-208`

Bearer token auth uses plain `==` string comparison, which is vulnerable to timing side-channel attacks:

```go
func (h *Handler) authorize(header string) bool {
    token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
    return token == h.cfg.Token  // NOT timing-safe
}
```

Should use `crypto/subtle.ConstantTimeCompare()`.

---

### 3. MEDIUM: Global Process Environment Mutation

**File:** `internal/stackcmd/stackcmd.go:96-100`

Calling `os.Setenv()` mutates global process state. If two deployments ever run concurrently (even though there's a mutex in runner, the CLI doesn't have one), environment variables from one deployment could leak into another:

```go
for k, v := range envValues {
    baseEnv[k] = v
    _ = os.Setenv(k, v)  // Mutates global process env
}
```

---

### 4. MEDIUM: Deployment Logs May Contain Secrets

**File:** `internal/runner/runner.go:105-106`

Full stdout/stderr from `docker compose` commands is logged unconditionally. If compose output contains interpolated secrets, credentials, or connection strings, they'll be written to logs:

```go
log.Printf("deployment stdout:\n%s", stdout.String())
log.Printf("deployment stderr:\n%s", stderr.String())
```

---

### 5. MEDIUM: No Timeout on Watcher Callback

**File:** `cmd/stackrd/main.go:79-104`

The file watcher callback runs `loadStackNames()`, `removalHandler.CheckForRemovals()`, and `scheduler.Reload()` synchronously with no timeout. A hung filesystem operation or Docker command blocks the entire watcher goroutine indefinitely.

---

### 6. MEDIUM: Network Cleanup Silently Swallows Errors

**File:** `internal/removal/cleanup.go:145-148`

When network removal fails, the error is logged as a warning but `nil` is returned, so the caller thinks cleanup succeeded:

```go
if output, err := rmCmd.CombinedOutput(); err != nil {
    log.Printf("warning: failed to remove some networks for stack %s: %s", stack, string(output))
    return nil  // Reports success despite failure
}
```

---

### 7. MEDIUM: Significant Code Duplication

Two independent copies of the same logic exist:

- **`labelMap` + `UnmarshalYAML`** — duplicated identically in `internal/httpapi/handler.go:41-71` AND `internal/cronjobs/scheduler.go:58-88`
- **`copyDir` + `copyFile`** — duplicated identically in `internal/removal/archive.go:82-138` AND `internal/stackcmd/stackcmd.go:876-930`

Bug fixes to one copy won't propagate to the other. These should be extracted to shared packages.

---

### 8. LOW-MEDIUM: Race Condition in Stack Tracking

**File:** `cmd/stackrd/main.go:79-104`

The watcher callback reads the stacks directory, checks for removals, then reloads cron — all without synchronization. If files change mid-callback, the cron scheduler and removal handler could have inconsistent views of which stacks exist.

---

## Prioritized Action Items

### Must Fix (Security)

1. **Path traversal in stack names** — One-line fix with big security payoff.
2. **Timing-safe token comparison** — Swap `==` for `crypto/subtle.ConstantTimeCompare()`. Trivial fix.
3. **Secrets in deployment logs** — Redact or avoid logging raw stdout/stderr.

### Should Fix (Reliability)

4. **Watcher callback timeout** — A single hung Docker command freezes the entire daemon.
5. **Global `os.Setenv()` mutation** — Works today due to mutex, but is a landmine for future changes.
6. **Network cleanup error swallowing** — Orphaned Docker networks will accumulate silently.

### Should Refactor (Maintainability)

7. **Duplicated `labelMap`/`UnmarshalYAML`** — Extract to shared package.
8. **Duplicated `copyDir`/`copyFile`** — Extract to shared package.

### Worth Investigating

9. **Race condition in watcher callback** — Removal handler and cron scheduler can see inconsistent state.
10. **Test coverage gaps** — No tests for `runner/`, `envfile/`, or `removal/` packages. These are the most critical paths.
