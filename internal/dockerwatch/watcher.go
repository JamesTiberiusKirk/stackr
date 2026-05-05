// Package dockerwatch listens to the Docker /events stream and turns
// container lifecycle events into stack-level status broadcasts on the
// realtime hub. The UI subscribes to `stack:<name>` and updates its
// status pills in place — no polling, no page refresh.
//
// Approach:
//
//   - Boot the daemon with a project→stack map (built from compose
//     files; "name:" field if present, directory name otherwise).
//   - Stream events filtered to type=container.
//   - Map each event's `com.docker.compose.project` label back to a
//     stack name; if unknown, drop.
//   - Debounce a few hundred ms (events flap during compose down→up),
//     then recompute that stack's status and publish.
//
// Failures (docker socket gone, events stream interrupted) get logged
// and retried with backoff; the watcher never panics out of Run.
package dockerwatch

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"

	"github.com/jamestiberiuskirk/stackr/internal/realtime"
	"github.com/jamestiberiuskirk/stackr/internal/service"
)

// debounceWindow is how long we wait after seeing an event before
// recomputing status — gives compose time to settle when a stack cycles
// (down emits multiple `die` events, then `start` events for `up`).
const debounceWindow = 250 * time.Millisecond

// reconnectBackoffMax caps the delay between retries when the events
// stream drops. The inner loop doubles from a small base; we don't go
// beyond ~30s so a recovered docker comes back to UI updates fast.
const reconnectBackoffMax = 30 * time.Second

// projectLabel is the docker-compose-supplied label every container in a
// compose project carries. Used to map events back to a stack.
const projectLabel = "com.docker.compose.project"

// StatusComputer narrows StackrService.StackStatus to what the watcher
// needs — an interface lets us avoid pulling the whole service into
// dockerwatch's import graph.
type StatusComputer interface {
	StackStatus(ctx context.Context, stack string) (service.StackStatus, error)
}

// Watcher is the long-running goroutine that bridges Docker events to
// realtime hub publications.
type Watcher struct {
	docker          *client.Client
	hub             *realtime.Hub
	status        StatusComputer
	projectToStack  map[string]string

	mu        sync.Mutex
	debounces map[string]*time.Timer
}

// New constructs a Watcher. projectToStack maps docker compose project
// names to stack names — pre-computed at daemon boot so each event has
// a fast O(1) lookup.
func New(docker *client.Client, hub *realtime.Hub, status StatusComputer, projectToStack map[string]string) *Watcher {
	return &Watcher{
		docker:         docker,
		hub:            hub,
		status:       status,
		projectToStack: projectToStack,
		debounces:      make(map[string]*time.Timer),
	}
}

// Run blocks until ctx is cancelled. Reconnects to the events stream on
// failure with exponential backoff.
func (w *Watcher) Run(ctx context.Context) {
	backoff := time.Second
	for {
		err := w.listen(ctx)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		if err != nil {
			slog.Warn("dockerwatch: events stream ended", "error", err, "retry_in", backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = nextBackoff(backoff)
	}
}

func nextBackoff(b time.Duration) time.Duration {
	next := b * 2
	if next > reconnectBackoffMax {
		next = reconnectBackoffMax
	}
	return next
}

// listen subscribes to the events stream and dispatches each event.
// Returns when the stream ends, ctx is cancelled, or an error occurs.
func (w *Watcher) listen(ctx context.Context) error {
	// Filter to container events and to lifecycle actions we care about.
	// Docker emits many events per minute (exec, attach, …) we don't
	// need; filtering at the API saves us the per-event noise.
	f := filters.NewArgs()
	f.Add("type", string(events.ContainerEventType))
	f.Add("event", "start")
	f.Add("event", "die")
	f.Add("event", "destroy")
	f.Add("event", "create")

	msgCh, errCh := w.docker.Events(ctx, events.ListOptions{Filters: f})
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-msgCh:
			if !ok {
				return io.EOF
			}
			w.dispatch(msg)
		case err, ok := <-errCh:
			if !ok {
				return io.EOF
			}
			return err
		}
	}
}

// dispatch maps a single docker event to its stack and schedules a
// debounced status recompute. We don't act on the event payload itself
// — we just know "something about this stack changed; re-fetch."
func (w *Watcher) dispatch(msg events.Message) {
	project := msg.Actor.Attributes[projectLabel]
	if project == "" {
		return
	}
	stack, ok := w.projectToStack[project]
	if !ok {
		return
	}
	w.scheduleRecompute(stack)
}

// scheduleRecompute defers the recompute by debounceWindow. If another
// event for the same stack arrives in that window, the earlier timer is
// reset — burstiness during compose down/up collapses to one publish.
func (w *Watcher) scheduleRecompute(stack string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if t, ok := w.debounces[stack]; ok {
		t.Stop()
	}
	w.debounces[stack] = time.AfterFunc(debounceWindow, func() {
		w.recompute(stack)
	})
}

// recompute calls into the service to fetch fresh status, then
// publishes it on the stack-scoped topic. Errors are logged but not
// retried — the next event will try again.
func (w *Watcher) recompute(stack string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	status, err := w.status.StackStatus(ctx, stack)
	if err != nil {
		slog.Warn("dockerwatch: status recompute failed", "stack", stack, "error", err)
		return
	}
	w.hub.Publish(realtime.Event{
		Topic: "stack:" + stack,
		Type:  "stack.status",
		Data: map[string]any{
			"stack":     status.Stack,
			"aggregate": status.Aggregate(),
			"running":   status.RunningCount(),
			"total":     status.TotalCount(),
			"services":  serializeServices(status.Services),
		},
	})
}

// serializeServices flattens ServiceStatus into JSON-friendly maps.
// Hand-rolled instead of a marshaller so we control the field naming
// the WS clients see (camelCase, public-only).
func serializeServices(in []service.ServiceStatus) []map[string]any {
	out := make([]map[string]any, 0, len(in))
	for _, s := range in {
		out = append(out, map[string]any{
			"name":      s.Name,
			"container": s.Container,
			"state":     s.State,
			"status":    s.Status,
			"image":     s.Image,
		})
	}
	return out
}

// projectFromName derives the docker-compose project name a compose
// file would land under. Order:
//   1. Explicit `name:` top-level field in the YAML.
//   2. Directory name, lowercased and stripped of unsafe characters
//      (compose's normalisation rules).
//
// Used at daemon boot to build the project→stack map.
func projectFromName(composeName, dirName string) string {
	if name := strings.TrimSpace(composeName); name != "" {
		return name
	}
	return normalizeProject(dirName)
}

// normalizeProject mirrors docker compose's lowercase + dash-only rule
// (digits/letters/dashes/underscores). Anything else collapses to a
// single dash. Good enough for our usage; the explicit `name:` field
// covers any stack with weird characters.
func normalizeProject(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	return b.String()
}
