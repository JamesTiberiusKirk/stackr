// Package jobs runs deferred work on behalf of HTTP handlers so request
// goroutines aren't blocked by long-running operations (docker compose
// up/down, image pulls, etc.). Each Enqueue spawns a goroutine; the
// caller gets a job ID immediately and can subscribe to lifecycle events
// via the realtime hub.
//
// Persistence is intentionally absent: the job queue is in-memory and
// resets on daemon restart. The "what happened" record lives in the
// deployments / cron_executions tables, populated by the operations
// themselves. Losing in-flight progress on a restart is acceptable —
// rows just stay in their last-recorded state (typically "running"
// without a finished_at).
package jobs

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Status values reported on every Job state change.
const (
	StatusPending = "pending"
	StatusRunning = "running"
	StatusSuccess = "success"
	StatusFailed  = "failed"
)

// Event is what publishers (handlers, cron, etc.) consume to decide what
// to push out to WS subscribers. Pure value type so the realtime hub
// doesn't need to import this package.
type Event struct {
	JobID     string
	Kind      string // operation kind: "stack.up", "stack.down", "cron.run", …
	Subject   string // target identifier: stack name, cron job key
	Status    string // Status* constant
	Message   string // human-readable summary; populated on finish
	Timestamp time.Time
}

// Publisher is the seam between jobs.Manager and a real broadcasting
// implementation (the realtime hub). Decoupled by interface so the
// jobs package depends on nothing except stdlib + uuid.
type Publisher interface {
	PublishJobEvent(Event)
}

// noopPublisher is the default — useful when the manager runs without a
// hub (tests, or daemon paths that don't care about live updates).
type noopPublisher struct{}

func (noopPublisher) PublishJobEvent(Event) {}

// Job is the externally-visible record of one queued task. Returned by
// Get; mutated only by the worker goroutine under the manager's mutex.
type Job struct {
	ID         string
	Kind       string
	Subject    string
	Status     string
	Message    string
	StartedAt  time.Time
	FinishedAt *time.Time
}

// Manager owns the in-memory job map and dispatches goroutines.
//
// The Manager is safe for concurrent Enqueue / Get; jobs run in their
// own goroutines and notify the publisher on each state change.
type Manager struct {
	mu        sync.Mutex
	jobs      map[string]*Job
	publisher Publisher
}

// New builds a Manager. publisher may be nil — a noop is wired in.
func New(publisher Publisher) *Manager {
	if publisher == nil {
		publisher = noopPublisher{}
	}
	return &Manager{
		jobs:      make(map[string]*Job),
		publisher: publisher,
	}
}

// Enqueue spawns a goroutine to run fn and returns the job id
// immediately. The kind/subject pair drives event topics on the realtime
// hub; pick stable values like "stack.up" + "<stack-name>".
//
// fn receives the job's lifetime context — currently always
// context.Background() since we don't expose a Cancel API yet. The
// signature is futureproofed so cancellation is a small additive change.
func (m *Manager) Enqueue(kind, subject string, fn func(ctx context.Context) error) string {
	id := uuid.New().String()
	now := time.Now()
	job := &Job{
		ID:        id,
		Kind:      kind,
		Subject:   subject,
		Status:    StatusPending,
		StartedAt: now,
	}

	m.mu.Lock()
	m.jobs[id] = job
	m.mu.Unlock()

	m.publisher.PublishJobEvent(Event{
		JobID:     id,
		Kind:      kind,
		Subject:   subject,
		Status:    StatusPending,
		Timestamp: now,
	})

	go m.run(job, fn)
	return id
}

// Get returns a copy of the named job, or nil if not found. Returning a
// value (not a pointer) prevents callers from racing with the worker
// that's still mutating the original.
func (m *Manager) Get(id string) *Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return nil
	}
	cp := *j
	return &cp
}

// run is the per-job goroutine body.
func (m *Manager) run(job *Job, fn func(ctx context.Context) error) {
	m.markRunning(job)

	err := fn(context.Background())
	finished := time.Now()

	m.mu.Lock()
	job.FinishedAt = &finished
	if err != nil {
		job.Status = StatusFailed
		job.Message = err.Error()
	} else {
		job.Status = StatusSuccess
	}
	final := *job
	m.mu.Unlock()

	m.publisher.PublishJobEvent(Event{
		JobID:     final.ID,
		Kind:      final.Kind,
		Subject:   final.Subject,
		Status:    final.Status,
		Message:   final.Message,
		Timestamp: finished,
	})
}

func (m *Manager) markRunning(job *Job) {
	now := time.Now()
	m.mu.Lock()
	job.Status = StatusRunning
	job.StartedAt = now
	snap := *job
	m.mu.Unlock()
	m.publisher.PublishJobEvent(Event{
		JobID:     snap.ID,
		Kind:      snap.Kind,
		Subject:   snap.Subject,
		Status:    StatusRunning,
		Timestamp: now,
	})
}
