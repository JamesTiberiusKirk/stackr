package cron

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jamestiberiuskirk/stackr/internal/db"
	"github.com/jamestiberiuskirk/stackr/internal/repo"
	"github.com/jamestiberiuskirk/stackr/internal/repo/sqlite"
	"github.com/jamestiberiuskirk/stackr/internal/runner"
)

// newRecorderTestStore returns a Recorder backed by a fresh in-memory SQLite
// store with the daemon's auto-migrations applied. We use the real store
// rather than a stub so GORM mappings (timestamp handling, nullable
// FinishedAt, the Error/Stdout text columns) are exercised end-to-end.
func newRecorderTestStore(t *testing.T) (*Recorder, repo.Store) {
	t.Helper()
	database, err := db.ConnectContext(context.Background(), ":memory:")
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(database))
	store := sqlite.NewStore(database)
	return NewRecorder(store), store
}

func TestRecorderStartCreatesRunningRow(t *testing.T) {
	r, store := newRecorderTestStore(t)
	ctx := context.Background()

	id, err := r.Start(ctx, "myapp", "worker", "* * * * *", repo.CronTriggerCron, "ctr-123")
	require.NoError(t, err)
	require.NotEmpty(t, id, "Start must return a row id so Finish can update it")

	row, err := store.GetCronExecutionByID(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, row, "Start must persist the row, not just allocate an id")

	require.Equal(t, id, row.ID)
	require.Equal(t, "myapp", row.Stack)
	require.Equal(t, "worker", row.Service)
	require.Equal(t, "* * * * *", row.Schedule)
	require.Equal(t, repo.CronStatusRunning, row.Status,
		"a freshly-started execution must be in 'running' state until Finish updates it")
	require.Equal(t, repo.CronTriggerCron, row.Trigger)
	require.Equal(t, "ctr-123", row.Container)
	require.False(t, row.StartedAt.IsZero(), "StartedAt must be set")
	require.Nil(t, row.FinishedAt, "FinishedAt must remain nil until Finish runs")
}

func TestRecorderFinishMarksSuccess(t *testing.T) {
	r, store := newRecorderTestStore(t)
	ctx := context.Background()

	id, err := r.Start(ctx, "myapp", "worker", "* * * * *", repo.CronTriggerManual, "")
	require.NoError(t, err)

	r.Finish(ctx, id, nil, "all done\n")

	row, err := store.GetCronExecutionByID(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, repo.CronStatusSuccess, row.Status)
	require.Equal(t, "all done\n", row.Stdout)
	require.Empty(t, row.Error)
	require.NotNil(t, row.FinishedAt, "FinishedAt must be set on success")
}

func TestRecorderFinishMarksFailure(t *testing.T) {
	r, store := newRecorderTestStore(t)
	ctx := context.Background()

	id, err := r.Start(ctx, "myapp", "worker", "* * * * *", repo.CronTriggerCron, "")
	require.NoError(t, err)

	r.Finish(ctx, id, errors.New("boom"), "partial output\n")

	row, err := store.GetCronExecutionByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, repo.CronStatusFailed, row.Status)
	require.Equal(t, "boom", row.Error,
		"plain errors should land verbatim — no extra wrapping")
	require.Equal(t, "partial output\n", row.Stdout,
		"stdout from a failed run must still be persisted for debuggability")
}

// TestRecorderFinishAppendsStderrFromCommandError pins the cron-recorder
// behavior of folding runner.CommandError.Stderr into the row.Error column.
// Cron jobs typically write the human-meaningful failure to stderr (the
// container's exit message) — losing it would defeat the table's purpose.
func TestRecorderFinishAppendsStderrFromCommandError(t *testing.T) {
	r, store := newRecorderTestStore(t)
	ctx := context.Background()

	id, err := r.Start(ctx, "myapp", "worker", "* * * * *", repo.CronTriggerCron, "")
	require.NoError(t, err)

	cmdErr := &runner.CommandError{
		Msg:    "compose run failed",
		Code:   1,
		Stderr: "container exited: out of memory",
	}
	r.Finish(ctx, id, cmdErr, "")

	row, err := store.GetCronExecutionByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, repo.CronStatusFailed, row.Status)
	require.Contains(t, row.Error, "container exited: out of memory",
		"stderr from CommandError must be folded into row.Error")
}

// TestRecorderFinishWithEmptyIDIsNoOp pins the contract that a failed Start
// (which returns id="") plus a follow-up Finish call doesn't blow up. This
// matters because the upstream cronjobs.Scheduler always calls Finish in a
// defer, regardless of whether Start succeeded; if Finish panicked on
// empty-id, a single store outage during Start would crash the scheduler.
func TestRecorderFinishWithEmptyIDIsNoOp(t *testing.T) {
	r, _ := newRecorderTestStore(t)
	ctx := context.Background()

	require.NotPanics(t, func() {
		r.Finish(ctx, "", errors.New("ignored"), "ignored")
	})
}
