package jobs

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// recordingPublisher captures every event for test assertions.
type recordingPublisher struct {
	mu     sync.Mutex
	events []Event
}

func (p *recordingPublisher) PublishJobEvent(e Event) {
	p.mu.Lock()
	p.events = append(p.events, e)
	p.mu.Unlock()
}

func (p *recordingPublisher) snapshot() []Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := make([]Event, len(p.events))
	copy(cp, p.events)
	return cp
}

func TestEnqueue_RunsAndReportsSuccess(t *testing.T) {
	p := &recordingPublisher{}
	m := New(p)

	done := make(chan struct{})
	id := m.Enqueue("stack.up", "nginx", func(_ context.Context) error {
		close(done)
		return nil
	})
	require.NotEmpty(t, id, "Enqueue must return a job id immediately so handlers can redirect with it")

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker goroutine did not run")
	}

	// Wait for finish event to be published. Brief poll because the
	// goroutine flips status after fn returns.
	require.Eventually(t, func() bool {
		j := m.Get(id)
		return j != nil && j.Status == StatusSuccess
	}, time.Second, 5*time.Millisecond)

	events := p.snapshot()
	require.GreaterOrEqual(t, len(events), 3, "must publish pending → running → success")
	require.Equal(t, StatusPending, events[0].Status)
	require.Equal(t, StatusRunning, events[1].Status)
	require.Equal(t, StatusSuccess, events[len(events)-1].Status)
}

func TestEnqueue_FailureRecordsErrorMessage(t *testing.T) {
	p := &recordingPublisher{}
	m := New(p)

	id := m.Enqueue("stack.up", "broken", func(_ context.Context) error {
		return errors.New("compose pull failed")
	})

	require.Eventually(t, func() bool {
		j := m.Get(id)
		return j != nil && j.Status == StatusFailed
	}, time.Second, 5*time.Millisecond)

	j := m.Get(id)
	require.Equal(t, "compose pull failed", j.Message,
		"failed jobs must surface fn's error verbatim — UI flashes this back to operators")
}

func TestGet_UnknownReturnsNil(t *testing.T) {
	m := New(nil)
	require.Nil(t, m.Get("does-not-exist"),
		"Get on a non-existent id must be nil so handlers can map to 404 cleanly")
}

func TestGet_ReturnsSnapshot(t *testing.T) {
	// Pinned because the worker mutates the underlying job; Get must
	// return a copy so callers reading concurrently can't race.
	m := New(nil)
	id := m.Enqueue("stack.up", "x", func(_ context.Context) error {
		time.Sleep(10 * time.Millisecond)
		return nil
	})
	got := m.Get(id)
	require.NotNil(t, got)
	got.Status = "tampered"

	require.Eventually(t, func() bool {
		fresh := m.Get(id)
		return fresh != nil && fresh.Status != "tampered"
	}, time.Second, 5*time.Millisecond)
}

func TestNoopPublisherIsDefault(t *testing.T) {
	// Manager works without a publisher — the daemon may construct one
	// before the realtime hub exists in tests.
	m := New(nil)
	require.NotPanics(t, func() {
		_ = m.Enqueue("kind", "subj", func(_ context.Context) error { return nil })
	})
}
