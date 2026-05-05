// Package reconcile turns "a stack file changed on disk" into "a deploy
// job for that stack." It sits between the file watcher and the action
// runner with a deliberate Plan / Apply seam so a future approval mode
// (terraform-style: see plan, click apply) can wedge in without
// touching the watcher or the runner.
//
// Today: file change → Plan returns one StackChange → Apply enqueues a
// job for it immediately.
//
// Tomorrow: the same Plan can be persisted to a `pending_changes` table
// and rendered for review; Apply runs only on operator consent.
//
// The package keeps its dependencies narrow on purpose. The Reconciler
// takes an `ActionEnqueuer` function rather than a service handle so
// the file-watching logic doesn't pull the whole runner / job graph
// into its import set.
package reconcile

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jamestiberiuskirk/stackr/internal/config"
	"github.com/jamestiberiuskirk/stackr/internal/watch"
)

// debounceWindow is per-stack, layered on top of WatchStacks' own
// debounce. Reasonable when a single edit produces multiple events
// (editors do save-then-rename dances) and we want one job per stack
// per burst.
const debounceWindow = 1 * time.Second

// ActionEnqueuer is the seam between the reconciler and the rest of
// the daemon. The closure passed in handles validation, audit row,
// docker compose call, etc. Reconciler only knows: "tell someone to
// run <action> on <stack>, the work happens elsewhere."
type ActionEnqueuer func(stack, action, trigger string) string

// Plan is the would-be effect of applying observed file changes. It's
// pure relative to its inputs — no mutations on the store, no jobs
// queued. Hand the same Plan to Apply to actually run.
type Plan struct {
	Stacks []StackChange
}

// IsEmpty reports whether the plan would do anything.
func (p Plan) IsEmpty() bool { return len(p.Stacks) == 0 }

// StackChange is the per-stack outcome of a Plan: "stack X needs
// action Y because reason Z." The reason string is freeform — used
// for /events log entries and the future approval-mode UI.
type StackChange struct {
	Stack  string
	Action string
	Reason string
}

// Reconciler watches stack files and enqueues jobs on change.
type Reconciler struct {
	cfg     config.Config
	enqueue ActionEnqueuer

	mu     sync.Mutex
	timers map[string]*time.Timer
}

// New constructs a Reconciler. The enqueue function is called from the
// reconcile goroutine; implementations are responsible for kicking the
// real work off-thread (today it goes through jobs.Manager which spawns
// per-job goroutines).
func New(cfg config.Config, enqueue ActionEnqueuer) *Reconciler {
	return &Reconciler{
		cfg:     cfg,
		enqueue: enqueue,
		timers:  make(map[string]*time.Timer),
	}
}

// Run blocks until ctx is canceled. Sets up the file watcher under the
// stacks dir; every event funnels through HandleEvent.
func (r *Reconciler) Run(ctx context.Context) error {
	return watch.WatchStacks(ctx, r.cfg.StacksDir, func(path string) {
		r.HandleEvent(path)
	})
}

// HandleEvent is the per-event entry point. Public so tests can drive
// the reconciler synchronously without spinning a real fsnotify watcher.
//
// Paths outside any stack directory are ignored. Events for a stack
// reset that stack's debounce timer so a burst of edits collapses into
// a single Plan+Apply.
func (r *Reconciler) HandleEvent(path string) {
	stack := PathToStack(r.cfg.StacksDir, path)
	if stack == "" {
		return
	}
	r.scheduleStack(stack)
}

// scheduleStack debounces per-stack. Subsequent events for the same
// stack within debounceWindow reset the timer, which avoids enqueueing
// a job per individual file in a multi-file edit.
func (r *Reconciler) scheduleStack(stack string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.timers[stack]; ok {
		t.Stop()
	}
	r.timers[stack] = time.AfterFunc(debounceWindow, func() {
		r.fire(stack)
	})
}

// fire runs Plan+Apply for the given stack. The seam: nothing prevents
// a future variant from persisting `plan` and gating the Apply behind
// an approval signal.
func (r *Reconciler) fire(stack string) {
	plan := PlanForStack(stack, "stack files changed")
	if plan.IsEmpty() {
		return
	}
	r.Apply(plan)
}

// PlanForStack computes the Plan implied by "stack X changed". Pure —
// exposed so tests and a future approval renderer can build plans
// without going through the reconciler's debounce logic.
func PlanForStack(stack, reason string) Plan {
	if stack == "" {
		return Plan{}
	}
	return Plan{Stacks: []StackChange{{
		Stack:  stack,
		Action: "up",
		Reason: reason,
	}}}
}

// Apply enqueues one job per StackChange and returns the resulting job
// IDs in input order. Trigger is fixed to "watch" so the audit row /
// event log can be filtered by source.
func (r *Reconciler) Apply(p Plan) []string {
	out := make([]string, 0, len(p.Stacks))
	for _, c := range p.Stacks {
		id := r.enqueue(c.Stack, c.Action, "watch")
		out = append(out, id)
	}
	return out
}

// PathToStack maps an fsnotify path back to its stack name. Returns ""
// for paths outside any stack directory (e.g. paths that don't sit
// under stacksDir, or top-level files in stacksDir itself).
//
// Convention: stacksDir contains one subdirectory per stack; a path is
// "in stack X" when its first component relative to stacksDir is X.
func PathToStack(stacksDir, fullPath string) string {
	if stacksDir == "" || fullPath == "" {
		return ""
	}
	abs, err := filepath.Abs(stacksDir)
	if err != nil {
		abs = stacksDir
	}
	cleaned := filepath.Clean(fullPath)
	rel, err := filepath.Rel(abs, cleaned)
	if err != nil || strings.HasPrefix(rel, "..") || rel == "." {
		return ""
	}
	// Only treat the path as a stack hit if it's inside a subdirectory —
	// a top-level file directly in stacksDir is debris (orphan README,
	// editor backup, etc.), not a stack-triggering edit.
	parts := strings.SplitN(rel, string(filepath.Separator), 2)
	if len(parts) < 2 || parts[0] == "" {
		return ""
	}
	return parts[0]
}
