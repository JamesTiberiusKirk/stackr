// Package background runs the daemon's long-lived non-HTTP services
// (cron scheduler, filesystem watcher, removal cleanup) and ties their
// lifecycle to the daemon process.
//
// The package is intentionally thin: it composes existing internal/* packages
// (cronjobs, watch, removal, stackcmd) and wires the daemon's recorder so
// every cron execution and stack-change event lands in the store.
package background

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/jamestiberiuskirk/stackr/internal/config"
	"github.com/jamestiberiuskirk/stackr/internal/cronjobs"
	"github.com/jamestiberiuskirk/stackr/internal/removal"
	"github.com/jamestiberiuskirk/stackr/internal/stackcmd"
	"github.com/jamestiberiuskirk/stackr/internal/watch"

	"github.com/jamestiberiuskirk/stackr/internal/cron"
	"github.com/jamestiberiuskirk/stackr/internal/repo"
)

// watchCallbackTimeout caps how long a single watch-callback iteration
// (reload cron, run removal cleanup) is allowed to take.
const watchCallbackTimeout = 2 * time.Minute

// Services bundles the cron scheduler, filesystem watcher, and removal
// cleanup. Construct via New, kick off via Start, tear down via Stop.
type Services struct {
	cfg     config.Config
	store   repo.Store
	log     *slog.Logger
	sched   *cronjobs.Scheduler
	rem     *removal.Handler

	mu          sync.Mutex
	started     bool
	watchCancel context.CancelFunc
}

// New constructs Services from a loaded stackr config. It does not yet start
// any goroutines — call Start once routes are registered and the HTTP server
// is about to come up.
func New(cfg config.Config, store repo.Store, log *slog.Logger) (*Services, error) {
	scheduler, err := cronjobs.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("init cron scheduler: %w", err)
	}
	scheduler.SetRecorder(cron.NewRecorder(store))

	rem := removal.NewHandler(cfg, removal.HandlerConfig{
		ContinueOnArchiveError: true,
		CleanupTimeout:         5 * time.Minute,
	})

	return &Services{
		cfg:   cfg,
		store: store,
		log:   log,
		sched: scheduler,
		rem:   rem,
	}, nil
}

// Start initializes the removal tracker, starts the cron scheduler, and spins
// up the filesystem watcher. Watcher and cron failures during startup are
// non-fatal: they're logged, persisted as events, and the daemon continues so
// the HTTP API still serves.
func (s *Services) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil
	}
	s.started = true

	if names, err := loadStackNames(s.cfg); err != nil {
		s.log.Warn("failed to load initial stack list", "error", err)
	} else {
		s.rem.Initialize(names)
	}

	if err := s.sched.Start(); err != nil {
		s.log.Error("failed to start cron scheduler", "error", err)
		s.recordEvent(ctx, repo.EventKindStackChanged, "", "cron scheduler failed to start: "+err.Error())
		return fmt.Errorf("start cron scheduler: %w", err)
	}

	watchCtx, cancel := context.WithCancel(ctx)
	s.watchCancel = cancel

	if err := watch.WatchStacks(watchCtx, s.cfg.StacksDir, func(path string) {
		s.handleStackChange(watchCtx, path)
	}); err != nil {
		s.log.Warn("stack watcher disabled", "error", err)
		s.recordEvent(ctx, repo.EventKindStackChanged, "", "stack watcher disabled: "+err.Error())
	}

	return nil
}

// Stop tears down background services in reverse order: cancel the watcher
// first so its callback can't kick off a reload mid-shutdown, then stop the
// cron scheduler.
func (s *Services) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return
	}
	if s.watchCancel != nil {
		s.watchCancel()
		s.watchCancel = nil
	}
	s.sched.Stop()
	s.started = false
}

// handleStackChange runs the post-change reconciliation: discover current
// stacks, run removal cleanup, reload the cron scheduler. Bounded by
// watchCallbackTimeout so a stuck cleanup can't wedge the watcher.
func (s *Services) handleStackChange(ctx context.Context, path string) {
	s.log.Info("stack change detected", "path", path)
	s.recordEvent(ctx, repo.EventKindStackChanged, "", "filesystem change at "+path)

	cbCtx, cancel := context.WithTimeout(ctx, watchCallbackTimeout)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		current, err := loadStackNames(s.cfg)
		if err != nil {
			s.log.Warn("failed to load current stacks", "error", err)
			return
		}
		s.rem.CheckForRemovals(current)
		if err := s.sched.Reload(); err != nil {
			s.log.Warn("failed to reload cron scheduler", "error", err)
		}
	}()

	select {
	case <-done:
	case <-cbCtx.Done():
		s.log.Warn("watch callback timed out", "timeout", watchCallbackTimeout)
	}
}

// recordEvent best-effort writes an Event row. Failures are logged but never
// propagate — observability must not block the caller.
func (s *Services) recordEvent(ctx context.Context, kind, stack, message string) {
	row := &repo.Event{
		ID:        uuid.New().String(),
		Kind:      kind,
		Stack:     stack,
		Message:   message,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.store.CreateEvent(ctx, row); err != nil {
		s.log.Warn("create event failed", "kind", kind, "stack", stack, "error", err)
	}
}

func loadStackNames(cfg config.Config) ([]string, error) {
	stacks, err := stackcmd.DiscoverStacks(cfg)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(stacks))
	for i, st := range stacks {
		names[i] = st.Name
	}
	return names, nil
}
