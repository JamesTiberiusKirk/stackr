package cron

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/jamestiberiuskirk/stackr/internal/runner"

	"github.com/jamestiberiuskirk/stackr/internal/repo"
)

// Recorder persists cron executions to the daemon's store. It implements
// cronjobs.Recorder so the upstream scheduler can drive lifecycle callbacks.
type Recorder struct {
	store repo.Store
}

// NewRecorder builds a Recorder backed by the given store.
func NewRecorder(store repo.Store) *Recorder {
	return &Recorder{store: store}
}

// Start writes a new CronExecution row with status=running and returns its ID.
// The id flows back into Finish so the row can be updated atomically.
func (r *Recorder) Start(ctx context.Context, stack, service, schedule, trigger, container string) (string, error) {
	id := uuid.New().String()
	row := &repo.CronExecution{
		ID:        id,
		Stack:     stack,
		Service:   service,
		Schedule:  schedule,
		Status:    repo.CronStatusRunning,
		Trigger:   trigger,
		Container: container,
		StartedAt: time.Now().UTC(),
	}
	if err := r.store.CreateCronExecution(ctx, row); err != nil {
		return "", fmt.Errorf("create cron_execution: %w", err)
	}
	return id, nil
}

// Finish updates the CronExecution row with the outcome. id may be empty if
// Start failed earlier — in that case Finish is a no-op so the upstream
// scheduler doesn't need a defensive nil-check.
func (r *Recorder) Finish(ctx context.Context, id string, runErr error, stdout string) {
	if id == "" {
		return
	}
	row, err := r.store.GetCronExecutionByID(ctx, id)
	if err != nil || row == nil {
		slog.Warn("cron recorder Finish: load row failed",
			"id", id, "err", err)
		return
	}

	finished := time.Now().UTC()
	row.FinishedAt = &finished
	row.Stdout = stdout
	if runErr != nil {
		row.Status = repo.CronStatusFailed
		row.Error = runErr.Error()
		var cmdErr *runner.CommandError
		if errors.As(runErr, &cmdErr) && cmdErr.Stderr != "" {
			row.Error = fmt.Sprintf("%s: %s", row.Error, cmdErr.Stderr)
		}
	} else {
		row.Status = repo.CronStatusSuccess
	}

	if err := r.store.UpdateCronExecution(ctx, row); err != nil {
		slog.Warn("cron recorder Finish: update row failed",
			"id", id, "err", err)
	}
}
