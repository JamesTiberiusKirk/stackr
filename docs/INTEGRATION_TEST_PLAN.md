# Integration Test Plan -- Testcontainers

## Overview

Add integration tests using `testcontainers-go` to validate critical paths that interact with Docker.
All integration tests use `//go:build integration` build tag so `go test ./...` remains fast.
Unit tests for `internal/envfile/` (no Docker needed) are included alongside.

## Dependencies

```
github.com/testcontainers/testcontainers-go v0.40.0
```

## Implementation Order

1. Add testcontainers dependency (`go get`)
2. Create `internal/testutil/testutil.go` -- shared helpers
3. Create `internal/envfile/envfile_test.go` -- unit tests (no Docker)
4. Create `internal/stackcmd/stackcmd_integration_test.go`
5. Create `internal/runner/runner_integration_test.go`
6. Create `internal/httpapi/handler_integration_test.go`
7. Create `internal/cronjobs/scheduler_integration_test.go`
8. Create `internal/removal/removal_integration_test.go`
9. Update `Makefile` -- add `test-integration` target
10. Update `.github/workflows/ci.yml` -- add integration test step
11. Run all tests and fix issues

---

## File 1: `internal/testutil/testutil.go`

Build tag: `//go:build integration`

Shared test helpers used across all integration test files.

### Functions

- `RequireDockerAvailable(t *testing.T)` -- calls `docker info`, skips test if Docker unavailable
- `SetupTestRepo(t *testing.T, opts ...RepoOption) string` -- creates temp dir with `.stackr.yaml`, `.env`, `stacks/<name>/docker-compose.yml`, configurable via functional options (stack name, compose content, env vars, pool paths, global config, etc.)
- `MinimalComposeYAML(serviceName, image string) string` -- returns a minimal compose YAML string
- `CleanupComposeProject(t *testing.T, projectDir string)` -- runs `docker compose down -v --remove-orphans` in `t.Cleanup()`

---

## File 2: `internal/envfile/envfile_test.go`

Build tag: none (pure unit tests, no Docker)

### Test 6.1: `TestSnapshotAndRestore`

Subtests:
- **HappyPath**: Write file, snapshot, modify, restore, verify content matches original
- **PreservesPermissions**: File with 0600 perms, snapshot + restore, verify perms preserved
- **FileNotFound**: Snapshot non-existent file, verify error

### Test 6.2: `TestUpdate`

Subtests:
- **ReplacesExistingKey**: File has `KEY=old`, update `KEY=new`, verify returns `old` and file has `KEY=new`
- **AppendsNewKey**: File has `OTHER=val`, update `KEY=new`, verify `KEY=new` appended
- **PreservesComments**: File has comments and blank lines, update a key, verify comments preserved
- **HandlesEqualsInValue**: `KEY=val=with=equals`, update KEY, verify correct parsing
- **NormalizesCRLF**: File has `\r\n` endings, update a key, verify `\r\n` removed
- **EmptyFile**: Update on empty file appends the key
- **TrailingNewline**: Verify output always ends with newline

---

## File 3: `internal/stackcmd/stackcmd_integration_test.go`

Build tag: `//go:build integration`

### Test 1.1: `TestComposeUpAndDown`

Full deploy lifecycle -- `Manager.Run()` with update action starts a compose stack, tear-down stops it.

1. Create temp repo with `.stackr.yaml`, `.env`, `stacks/testapp/docker-compose.yml` (nginx service)
2. Build `config.Config` pointing at temp repo
3. Create `stackcmd.Manager` via `NewManagerWithWriters()` (capture stdout/stderr)
4. Call `Manager.Run()` with `Options{Stacks: ["testapp"], Update: true}`
5. Assert: nginx container is running (via Docker API)
6. Call `Manager.Run()` with `Options{Stacks: ["testapp"], TearDown: true}`
7. Assert: container stopped and removed

### Test 1.2: `TestEnvVarHandling`

11 subtests covering all env var scenarios.

**1.2a: `CustomVarsInjected`**
- `.env` has `MY_TAG=v1.2.3`, compose uses `image: nginx:${MY_TAG}`
- Run with update, verify container running with correct image tag

**1.2b: `AutoProvisionedVars`**
- Config has `pool_paths`, `base_domain`
- Compose uses `${STACKR_PROV_DOMAIN}`, `${STACKR_PROV_POOL_SSD}`, `${STACKR_PROV_POOL_HDD}`
- Run dry-run/config, verify resolved values in output

**1.2c: `StackIsolation`**
- Two stacks with separate env vars
- Deploy only stackA, verify stackB vars don't leak into stackA

**1.2d: `GlobalVarsSharedAcrossStacks`**
- `.stackr.yaml` has `global_env: {SHARED_KEY: shared_value}`
- Both stacks reference `${SHARED_KEY}`, verify both receive it

**1.2e: `MissingVarFailsValidation`**
- Compose references `${UNDEFINED_VAR}` not in `.env`
- Run with update, verify error about missing variable

**1.2f: `OfflineStackSkipped`**
- `.env` has `TESTAPP_OFFLINE=true`
- Run with update, verify no container started

**1.2g: `PoolPathsResolvedCorrectly`**
- Config has `pools: {ssd: .ssd_pool, hdd: .hdd_pool}` (relative)
- Deploy stack, verify:
  - `STACKR_PROV_POOL_SSD` = `<repoRoot>/.ssd_pool/<stackName>`
  - `STACKR_PROV_POOL_HDD` = `<repoRoot>/.hdd_pool/<stackName>`
  - Directories actually created on disk

**1.2h: `AbsolutePoolPathsPreserved`**
- Config has `pools: {ssd: /tmp/test-ssd}` (absolute)
- Verify pool path is `/tmp/test-ssd/<stackName>` (not joined with repoRoot)

**1.2i: `LegacyStorageVarsSetFromPools`**
- Config has pools, compose references `${STACK_STORAGE_SSD}` and `${STACK_STORAGE_HDD}`
- Verify legacy vars resolve to same paths as `STACKR_PROV_POOL_*`

**1.2j: `CustomPathVarsFromConfig`**
- `.stackr.yaml` has `paths: {custom: {MY_DATA_DIR: /opt/data, MY_CACHE_DIR: ./cache}}`
- Compose references `${MY_DATA_DIR}` and `${MY_CACHE_DIR}`
- Verify values passed through correctly

**1.2k: `HostRepoRootPathTranslation`**
- Set `STACKR_HOST_REPO_ROOT=/host/path/to/repo`
- `RepoRoot` is actual temp dir
- Verify manager correctly distinguishes host vs container paths

### Test 1.3: `TestGetVarsAppendsToEnvFile`

1. Compose references `${APP_PORT}`, `${DB_HOST}`, `${STACKR_PROV_DOMAIN}`
2. `.env` starts with only `APP_PORT=8080`
3. Run with `Options{GetVars: true}`
4. Assert:
   - `APP_PORT=8080` unchanged
   - `DB_HOST=` appended as stub
   - `STACKR_PROV_DOMAIN` NOT appended (auto-provisioned)

### Test 1.4: `TestBackupStack`

6 subtests.

**1.4a: `BacksUpConfigDirectories`**
- Stack has `config/`, `dashboards/`, `dynamic/` with files
- Run backup, assert all copied to `backups/<timestamp>/<stack>/`

**1.4b: `BacksUpPoolVolumes`**
- Pools have data in `<pool>/<stack>/`
- Run backup, assert pool data in `backups/<timestamp>/<stack>/pool_ssd/` and `pool_hdd/`

**1.4c: `SkipsMissingDirectories`**
- Stack only has `config/`, no `dashboards/` or `dynamic/`
- Run backup, assert only `config/` backed up, no error

**1.4d: `DryRunDoesNotCopy`**
- Run backup with `DryRun: true`
- Assert no backup directory created, stdout contains `[DRY RUN]`

**1.4e: `BackupDirNotSet`**
- Empty backup_dir in config
- Assert error "BACKUP_DIR is not set"

**1.4f: `TimestampedDirectoryCreated`**
- Run backup twice
- Assert two separate timestamped directories exist

---

## File 4: `internal/runner/runner_integration_test.go`

Build tag: `//go:build integration`

### Test 2.1: `TestDeploySuccess`

1. Create temp repo with working compose stack (nginx)
2. Create `runner.Runner` with config
3. Call `Deploy(ctx, "testapp", stackCfg, "v1.0.0")`
4. Assert: Result has `Status: "ok"`, `Stack: "testapp"`, `Tag: "v1.0.0"`
5. Assert: Stdout contains compose output
6. Assert: Container actually running
7. Cleanup: tear down

### Test 2.2: `TestDeployFailureRollsBackEnv`

1. Create temp repo with compose file referencing non-existent image
2. `.env` starts with `IMAGE_TAG=v1.0.0`
3. Call `Deploy(ctx, "testapp", stackCfg, "v2.0.0")` -- updates .env to v2.0.0, compose fails
4. Assert: returns `CommandError`
5. Assert: `.env` restored to `IMAGE_TAG=v1.0.0`

### Test 2.3: `TestDeployConcurrentSerialization`

1. Create temp repo with working compose stack
2. Launch two goroutines calling `Deploy()` concurrently
3. Assert: both complete without error
4. Assert: they didn't run simultaneously (detect via timing or channels)
5. Assert: final `.env` reflects last deploy's tag

---

## File 5: `internal/httpapi/handler_integration_test.go`

Build tag: `//go:build integration`

### Test 3.1: `TestHealthEndpoint`

1. Create handler with `httpapi.New(cfg, runner)` using real runner
2. `httptest.NewServer`
3. GET `/healthz`
4. Assert: 200, `{"status":"ok"}`

### Test 3.2: `TestDeployEndpoint`

8 subtests.

**3.2a: `MissingAuthReturns401`** -- POST `/deploy` without auth header, assert 401

**3.2b: `WrongTokenReturns401`** -- POST with wrong bearer token, assert 401

**3.2c: `InvalidBodyReturns400`** -- POST with malformed JSON, assert 400

**3.2d: `MissingStackReturns400`** -- POST with `{"tag":"v1.0.0"}` (no stack), assert 400

**3.2e: `NonExistentStackReturns404`** -- POST with non-existent stack name, assert 404

**3.2f: `AutoDeployDisabledReturns403`** -- Stack has `stackr.deploy.auto: "false"` label, assert 403

**3.2g: `SuccessfulDeployReturns200`** -- Valid auth + stack + tag, assert 200 + container running

**3.2h: `InvalidTagFormatReturns400`** -- POST with `"tag": "not a valid tag!!!"`, assert 400

---

## File 6: `internal/cronjobs/scheduler_integration_test.go`

Build tag: `//go:build integration`

### Test 4.1: `TestExecuteJobManually`

1. Compose file with service having `stackr.cron.schedule` label, alpine image, `echo hello-from-cron`
2. Call `ExecuteJobManually(cfg, "testapp", "worker", nil)`
3. Assert: no error, log files created, container ran and exited

### Test 4.2: `TestExecuteJobManuallyWithCustomCommand`

1. Same compose setup
2. Call `ExecuteJobManually(cfg, "testapp", "worker", []string{"echo", "custom-output"})`
3. Assert: custom command used instead of default

### Test 4.3: `TestCleanupOldContainers`

1. Create 5 containers with `testapp-worker-cron-<timestamp>` naming via Docker API
2. Call `CleanupOldContainers(3)` (retain 3)
3. Assert: only 3 newest remain, 2 oldest removed

---

## File 7: `internal/removal/removal_integration_test.go`

Build tag: `//go:build integration`

### Test 5.1: `TestTrackerDetectsRemovals`

1. Initialize tracker with `["stackA", "stackB", "stackC"]`
2. `Update(["stackA", "stackC"])` -- assert returns `["stackB"]`
3. `Update(["stackA", "stackC"])` again -- assert returns empty

### Test 5.2: `TestCleanupWithComposeFile`

1. Create compose stack, bring it up (containers, volumes, networks created)
2. Call `removal.Cleanup(ctx, "testapp", stacksDir)` while compose file exists
3. Assert: containers, volumes, networks removed

### Test 5.3: `TestCleanupWithoutComposeFile`

1. Create compose stack, bring it up
2. Delete compose file
3. Call `removal.Cleanup(ctx, "testapp", stacksDir)`
4. Assert: resources removed via label-based cleanup

### Test 5.4: `TestArchiveBeforeCleanup`

1. Stack with config dirs and pool data
2. Call `removal.Archive("testapp", archiveCfg)`
3. Assert: archive created under `backups/archives/<timestamp>/testapp/`
4. Call `Cleanup()` after archive
5. Assert: Docker resources gone but archive preserved

---

## Makefile Addition

```makefile
test-integration:
	go test -v -tags=integration -timeout=10m ./...
```

## CI Workflow Addition

Add step to `.github/workflows/ci.yml` after unit tests:

```yaml
- name: Integration Tests
  run: make test-integration
```

GitHub Actions runners have Docker pre-installed.

---

## Summary

| File | Build Tag | Tests | Subtests |
|---|---|---|---|
| `internal/testutil/testutil.go` | integration | - | shared helpers |
| `internal/envfile/envfile_test.go` | none | 2 | 10 |
| `internal/stackcmd/stackcmd_integration_test.go` | integration | 4 | 17+ |
| `internal/runner/runner_integration_test.go` | integration | 3 | 0 |
| `internal/httpapi/handler_integration_test.go` | integration | 2 | 9 |
| `internal/cronjobs/scheduler_integration_test.go` | integration | 3 | 0 |
| `internal/removal/removal_integration_test.go` | integration | 4 | 0 |
| **Total** | | **18** | **36+** |
