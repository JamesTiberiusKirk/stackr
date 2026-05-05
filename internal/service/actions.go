package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/jamestiberiuskirk/stackr/internal/repo"
	"github.com/jamestiberiuskirk/stackr/internal/stackcmd"
)

// Action verb constants. Used as both the deployment row's Tag column
// (so the UI can show "up" / "down" / "restart" / "update" inline with
// the deploy log) and as the routes the /stacks/{name}/{action}
// handler accepts.
const (
	ActionUp      = "up"
	ActionDown    = "down"
	ActionRestart = "restart"
	ActionUpdate  = "update"
)

// ErrInvalidAction is returned when the HTTP handler is asked to run an
// action verb the service doesn't recognise. Helps the handler return
// 400 instead of falling through to a 500.
var ErrInvalidAction = errors.New("invalid stack action")

// BringUp runs `docker compose up -d` against the stack. trigger is one
// of the repo.DeploymentTrigger* constants — "manual" for button clicks,
// "api" for direct API calls.
func (s *StackrService) BringUp(ctx context.Context, stack, trigger string) (*repo.Deployment, error) {
	return s.runStackAction(ctx, stack, ActionUp, trigger, stackcmd.Options{})
}

// BringDown runs `docker compose down`.
func (s *StackrService) BringDown(ctx context.Context, stack, trigger string) (*repo.Deployment, error) {
	return s.runStackAction(ctx, stack, ActionDown, trigger, stackcmd.Options{TearDown: true})
}

// UpdateStack runs `docker compose pull` then up if any image changed.
// Skips bringing the stack up if all images are already current and the
// stack isn't fully running — see runStack in stackcmd for the early-
// exit logic. Operators wanting "ensure up" should use BringUp.
func (s *StackrService) UpdateStack(ctx context.Context, stack, trigger string) (*repo.Deployment, error) {
	return s.runStackAction(ctx, stack, ActionUpdate, trigger, stackcmd.Options{Update: true})
}

// RestartStack tears the stack down and brings it back up. We do this
// as two manager.Run calls rather than one combined op so the logs are
// clearer ("down" then "up" in stdout) and a failure on down is
// distinguishable from a failure on up.
func (s *StackrService) RestartStack(ctx context.Context, stack, trigger string) (*repo.Deployment, error) {
	if _, err := s.GetStack(ctx, stack); err != nil {
		return nil, err
	}

	row := s.beginDeploymentRow(ctx, stack, ActionRestart, trigger)

	var combined bytes.Buffer
	if err := s.execManagerRun(ctx, stack, stackcmd.Options{TearDown: true}, &combined); err != nil {
		return s.finishDeploymentRow(ctx, row, combined.String(), err)
	}
	combined.WriteString("\n--- bringing up ---\n")
	err := s.execManagerRun(ctx, stack, stackcmd.Options{}, &combined)
	return s.finishDeploymentRow(ctx, row, combined.String(), err)
}

// PerformAction is the dispatcher used by the HTTP handler — keeps the
// action-verb → method mapping in one place so the route signature
// stays a simple `(name, action, trigger)`.
func (s *StackrService) PerformAction(ctx context.Context, stack, action, trigger string) (*repo.Deployment, error) {
	switch action {
	case ActionUp:
		return s.BringUp(ctx, stack, trigger)
	case ActionDown:
		return s.BringDown(ctx, stack, trigger)
	case ActionRestart:
		return s.RestartStack(ctx, stack, trigger)
	case ActionUpdate:
		return s.UpdateStack(ctx, stack, trigger)
	}
	return nil, fmt.Errorf("%w: %q", ErrInvalidAction, action)
}

// runStackAction is the shared body for BringUp / BringDown / UpdateStack:
//
//   - validate stack exists,
//   - record a deployment row in "running" state,
//   - run stackcmd.Manager.Run with the supplied options and capture I/O,
//   - finalise the row with "success" / "failed" + stdout + error text.
//
// RestartStack inlines the two-step variant rather than using this helper.
func (s *StackrService) runStackAction(ctx context.Context, stack, action, trigger string, opts stackcmd.Options) (*repo.Deployment, error) {
	if _, err := s.GetStack(ctx, stack); err != nil {
		return nil, err
	}
	row := s.beginDeploymentRow(ctx, stack, action, trigger)

	var combined bytes.Buffer
	err := s.execManagerRun(ctx, stack, opts, &combined)
	return s.finishDeploymentRow(ctx, row, combined.String(), err)
}

// beginDeploymentRow inserts a row in DeploymentStatusRunning state. If
// the insert itself fails we return a non-persisted row — the action
// still runs, but its outcome won't be recorded. That's preferable to
// blocking the operator on a transient store outage.
func (s *StackrService) beginDeploymentRow(ctx context.Context, stack, action, trigger string) *repo.Deployment {
	row := &repo.Deployment{
		ID:        uuid.New().String(),
		Stack:     stack,
		Tag:       action,
		Status:    repo.DeploymentStatusRunning,
		Trigger:   trigger,
		StartedAt: time.Now(),
	}
	if err := s.store.CreateDeployment(ctx, row); err != nil {
		// Don't fail the action — log via the row's Error so the caller
		// can see why audit is missing, but still return a usable row.
		row.Error = "audit: create row: " + err.Error()
	}
	return row
}

// finishDeploymentRow updates the row with the captured stdout and
// final status. Truncated stdout (avoiding multi-megabyte rows that
// blow up the SQLite file) is left for a later concern — at homelab
// scale rows are small.
func (s *StackrService) finishDeploymentRow(ctx context.Context, row *repo.Deployment, stdout string, runErr error) (*repo.Deployment, error) {
	finished := time.Now()
	row.FinishedAt = &finished
	row.Stdout = strings.TrimSpace(stdout)
	if runErr != nil {
		row.Status = repo.DeploymentStatusFailed
		row.Error = runErr.Error()
	} else {
		row.Status = repo.DeploymentStatusSuccess
	}
	if err := s.store.UpdateDeployment(ctx, row); err != nil {
		// Update failure is non-fatal for the same reason as create —
		// the action ran successfully or not, the operator's UI sees
		// the result via the returned row.
		row.Error = appendErr(row.Error, "audit: update row: "+err.Error())
	}
	return row, runErr
}

// execManagerRun invokes stackcmd.Manager.Run for a single stack with
// captured stdout+stderr piped into combined. Used by all four actions
// so they share log capture + path resolution behaviour.
func (s *StackrService) execManagerRun(ctx context.Context, stack string, opts stackcmd.Options, combined *bytes.Buffer) error {
	manager, err := stackcmd.NewManagerWithWriters(s.cfg, combined, combined)
	if err != nil {
		return fmt.Errorf("create stack manager: %w", err)
	}
	opts.Stacks = []string{stack}
	return manager.Run(ctx, opts)
}

// appendErr concatenates two error strings with a separator if both are
// non-empty. Avoids "; " noise when only one half exists.
func appendErr(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "; " + b
}
