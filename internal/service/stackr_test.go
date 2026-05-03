package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/jamestiberiuskirk/stackr/internal/config"
	"github.com/jamestiberiuskirk/stackr/internal/db"
	"github.com/jamestiberiuskirk/stackr/internal/repo/sqlite"
	"github.com/jamestiberiuskirk/stackr/internal/stackcmd"
)

// setupDeployTest builds a StackrService backed by an in-memory SQLite store,
// rooted at a tmp repo with one local stack named "app". stackrComposeBody
// lets each case override the docker-compose.yml — useful for toggling
// auto-deploy via labels. The returned *gorm.DB lets a test poison the
// connection (close it) to exercise store-failure paths.
func setupDeployTest(t *testing.T, stackrComposeBody string) (*StackrService, *gorm.DB) {
	t.Helper()

	root := t.TempDir()
	stacksDir := filepath.Join(root, "stacks")
	require.NoError(t, os.MkdirAll(filepath.Join(stacksDir, "app"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(stacksDir, "app", "docker-compose.yml"),
		[]byte(stackrComposeBody),
		0o644,
	))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".env"), []byte(""), 0o644))

	cfg := config.Config{
		RepoRoot:  root,
		StacksDir: stacksDir,
		EnvFile:   filepath.Join(root, ".env"),
		Token:     "test-token",
	}

	database, err := db.ConnectContext(context.Background(), ":memory:")
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(database))

	return NewStackrService(cfg, sqlite.NewStore(database)), database
}

const (
	enabledCompose = `services:
  web:
    image: nginx:alpine
`
	disabledCompose = `services:
  web:
    image: nginx:alpine
    labels:
      stackr.deploy.auto: "false"
`
)

// TestDeploy_FailFastValidation covers the validation branches that reject
// before the runner is invoked. Two patterns are exercised here:
//
//   - Direct rejection: Deploy returns the typed sentinel and a nil row
//     (e.g. ErrInvalidStackName, ErrInvalidTag, ErrStackNotFound).
//
//   - Indirect "regex accepted" verification: a stack with auto-deploy=false
//     forces a known fail-stop AFTER the regex gate. If a future change
//     breaks the regex (e.g. a "v"-prefix typo) the test will see
//     ErrInvalidTag instead of ErrAutoDeployDisabled, and fail. This way
//     the regex's accept side is tested through the actual call chain
//     without invoking the runner.
func TestDeploy_FailFastValidation(t *testing.T) {
	tests := []struct {
		name        string
		stack       string
		tag         string
		composeBody string
		wantErrIs   error
		wantErrSub  string
	}{
		// --- Stack-name validation ---
		{
			name:        "unknown stack returns ErrStackNotFound",
			stack:       "doesnotexist",
			tag:         "v1.0.0",
			composeBody: enabledCompose,
			wantErrIs:   ErrStackNotFound,
		},
		{
			name:        "path traversal rejected as invalid name",
			stack:       "../etc",
			tag:         "v1.0.0",
			composeBody: enabledCompose,
			wantErrIs:   ErrInvalidStackName,
		},
		{
			name:        "leading whitespace rejected as invalid name",
			stack:       "  app",
			tag:         "v1.0.0",
			composeBody: enabledCompose,
			wantErrIs:   ErrInvalidStackName,
		},
		{
			name:        "literal dot rejected as invalid name",
			stack:       ".",
			tag:         "v1.0.0",
			composeBody: enabledCompose,
			wantErrIs:   ErrInvalidStackName,
		},
		{
			name:        "empty stack falls through to runner-stage required check",
			stack:       "",
			tag:         "v1.0.0",
			composeBody: enabledCompose,
			wantErrIs:   ErrInvalidStackName,
		},

		// --- Tag validation: rejection side ---
		{
			name:        "empty tag returns generic required error",
			stack:       "app",
			tag:         "",
			composeBody: enabledCompose,
			wantErrSub:  "tag is required",
		},
		{
			name:        "whitespace-only tag treated as empty",
			stack:       "app",
			tag:         "   ",
			composeBody: enabledCompose,
			wantErrSub:  "tag is required",
		},
		{
			name:        "tag without v prefix rejected",
			stack:       "app",
			tag:         "1.0.0",
			composeBody: enabledCompose,
			wantErrIs:   ErrInvalidTag,
		},
		{
			name:        "incomplete semver rejected",
			stack:       "app",
			tag:         "v1.0",
			composeBody: enabledCompose,
			wantErrIs:   ErrInvalidTag,
		},
		{
			name:        "build metadata rejected (we accept only -prerelease)",
			stack:       "app",
			tag:         "v1.0.0+build42",
			composeBody: enabledCompose,
			wantErrIs:   ErrInvalidTag,
		},
		{
			name:        "garbage tag rejected",
			stack:       "app",
			tag:         "not a tag!!",
			composeBody: enabledCompose,
			wantErrIs:   ErrInvalidTag,
		},
		{
			name:        "tag with trailing whitespace rejected",
			stack:       "app",
			tag:         "v1.0.0 extra",
			composeBody: enabledCompose,
			wantErrIs:   ErrInvalidTag,
		},

		// --- Tag validation: indirect "regex accepted" via auto-deploy=false ---
		// If any of these regress to ErrInvalidTag, the regex's accept side broke.
		{
			name:        "tag latest passes regex (hits auto-deploy gate)",
			stack:       "app",
			tag:         "latest",
			composeBody: disabledCompose,
			wantErrIs:   ErrAutoDeployDisabled,
		},
		{
			name:        "tag v1.0.0 passes regex (hits auto-deploy gate)",
			stack:       "app",
			tag:         "v1.0.0",
			composeBody: disabledCompose,
			wantErrIs:   ErrAutoDeployDisabled,
		},
		{
			name:        "tag v1.2.3-rc1 passes regex (hits auto-deploy gate)",
			stack:       "app",
			tag:         "v1.2.3-rc1",
			composeBody: disabledCompose,
			wantErrIs:   ErrAutoDeployDisabled,
		},
		{
			name:        "tag v10.20.30-prerelease.1 passes regex (hits auto-deploy gate)",
			stack:       "app",
			tag:         "v10.20.30-prerelease.1",
			composeBody: disabledCompose,
			wantErrIs:   ErrAutoDeployDisabled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := setupDeployTest(t, tt.composeBody)

			d, err := svc.Deploy(context.Background(), tt.stack, tt.tag, "test")

			require.Error(t, err)
			require.Nil(t, d, "no deployment row should be returned for fail-fast errors")

			if tt.wantErrIs != nil {
				require.True(t, errors.Is(err, tt.wantErrIs),
					"expected error to wrap %v, got %v", tt.wantErrIs, err)
			}
			if tt.wantErrSub != "" {
				require.Contains(t, err.Error(), tt.wantErrSub)
			}
		})
	}
}

// TestDeploy_StackcmdSentinelFlowsThrough pins the contract that
// stackcmd.ErrInvalidStackName errors get translated to service.ErrInvalidStackName,
// not service.ErrStackNotFound. This is the wire that lets the deploy handler
// pick 400 over 404 — if it breaks, malformed names start surfacing as
// "stack not found" again.
func TestDeploy_StackcmdSentinelFlowsThrough(t *testing.T) {
	svc, _ := setupDeployTest(t, enabledCompose)

	_, err := svc.Deploy(context.Background(), "../etc", "v1.0.0", "test")

	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidStackName)
	require.NotErrorIs(t, err, ErrStackNotFound,
		"invalid name must NOT be reported as not-found — they map to different HTTP statuses")
	require.ErrorIs(t, err, stackcmd.ErrInvalidStackName,
		"the underlying stackcmd sentinel should still be reachable via errors.Is")
}

// TestDeploy_StoreCreateFailure exercises the store.CreateDeployment failure
// branch: validation passes, the runner has not yet been invoked, but the
// store rejects the row insert (here simulated by closing the underlying
// SQLite connection). The service must surface the wrapped error and not
// invent a deployment row.
func TestDeploy_StoreCreateFailure(t *testing.T) {
	svc, database := setupDeployTest(t, enabledCompose)

	// Poison the store: close the SQLite connection so any Create/Update
	// returns "sql: database is closed". This proves the error surface
	// without needing a stub Store implementation.
	sqlDB, err := database.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	d, err := svc.Deploy(context.Background(), "app", "v1.0.0", "test")

	require.Error(t, err)
	require.Nil(t, d)
	require.Contains(t, err.Error(), "create deployment row",
		"the wrapped error must mention which step failed so logs are diagnosable")
}
