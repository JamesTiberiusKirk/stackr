// Package realtime is the daemon's pub/sub hub for live UI updates.
// Topic-based: publishers (job manager, status poller, …) call Publish
// with a topic + event; subscribers (websocket clients) get every event
// matching the topics they subscribed to.
//
// Each subscriber owns a buffered channel; if a slow consumer fills the
// buffer the event is dropped on the floor for that subscriber rather
// than blocking the publisher. Live data is recoverable on next
// publish, so dropping is preferable to back-pressuring everyone else.
package realtime

import (
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/jamestiberiuskirk/stackr/internal/jobs"
)

// subscriberBuffer is how many events queue per subscriber before the
// hub starts dropping. Tuned so a typical browser refresh-pause won't
// lose events but a hung connection can't pile up forever.
const subscriberBuffer = 32

// Event is the value broadcast over the hub. Type names what happened
// ("job.pending", "job.success", "stack.status", …); Topic decides who
// hears it. Data is opaque JSON-encodable payload — kept generic so the
// hub doesn't grow per-feature fields.
type Event struct {
	Topic     string
	Type      string
	Data      any
	Timestamp time.Time
}

// Hub is the central broadcaster. Subscribers register, the hub fans
// out events to matching subscribers, the broadcaster goroutine of any
// publisher returns immediately.
type Hub struct {
	mu          sync.RWMutex
	subscribers map[string]*subscriber
}

type subscriber struct {
	id     string
	topics map[string]struct{}
	out    chan Event
}

// New constructs a fresh Hub.
func New() *Hub {
	return &Hub{subscribers: make(map[string]*subscriber)}
}

// Subscribe registers a new consumer for the given topics. Returns an
// id (used to unsubscribe) and a receive-only channel of matching
// events. Topics is copied so callers can mutate their own slice
// freely after subscribing.
func (h *Hub) Subscribe(topics ...string) (string, <-chan Event) {
	id := uuid.New().String()
	sub := &subscriber{
		id:     id,
		topics: make(map[string]struct{}, len(topics)),
		out:    make(chan Event, subscriberBuffer),
	}
	for _, t := range topics {
		sub.topics[t] = struct{}{}
	}
	h.mu.Lock()
	h.subscribers[id] = sub
	h.mu.Unlock()
	return id, sub.out
}

// Unsubscribe removes the consumer and closes its channel. Safe to
// call multiple times — second + subsequent calls are a no-op.
func (h *Hub) Unsubscribe(id string) {
	h.mu.Lock()
	sub, ok := h.subscribers[id]
	if ok {
		delete(h.subscribers, id)
	}
	h.mu.Unlock()
	if ok {
		close(sub.out)
	}
}

// AddTopic adds an extra topic to an existing subscription. Useful when
// the WS client subscribes to a job mid-stream (e.g. after triggering an
// action and learning its job id from the redirect).
func (h *Hub) AddTopic(id, topic string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if sub, ok := h.subscribers[id]; ok {
		sub.topics[topic] = struct{}{}
	}
}

// Publish fans event out to every subscriber whose topic set contains
// event.Topic. Slow consumers are dropped (via the default branch in
// the select) rather than back-pressuring the publisher.
func (h *Hub) Publish(e Event) {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, sub := range h.subscribers {
		if _, ok := sub.topics[e.Topic]; !ok {
			continue
		}
		select {
		case sub.out <- e:
		default:
			// Subscriber's buffer full — drop the event for this one
			// consumer. Other subscribers still receive it.
		}
	}
}

// PublishJobEvent satisfies jobs.Publisher. The hub maps a Job event
// onto three topics:
//   - `job:<id>`        — clients tracking a specific operation
//   - `stack:<name>` /  — page-level subscribers (stack detail, cron view)
//     `cron:<key>`
//   - `events`          — the global firehose, for the layout's
//                         notification banner that shows every action
//                         regardless of which page the operator is on.
func (h *Hub) PublishJobEvent(e jobs.Event) {
	payload := map[string]any{
		"jobId":   e.JobID,
		"kind":    e.Kind,
		"subject": e.Subject,
		"status":  e.Status,
		"message": e.Message,
	}
	envelope := Event{
		Type:      "job." + e.Status,
		Data:      payload,
		Timestamp: e.Timestamp,
	}

	envelope.Topic = "job:" + e.JobID
	h.Publish(envelope)
	if e.Subject != "" {
		envelope.Topic = subjectTopic(e.Kind, e.Subject)
		h.Publish(envelope)
	}
	envelope.Topic = "events"
	h.Publish(envelope)
}

// subjectTopic picks the right topic prefix from the job kind. New
// kinds default to "subject:<name>" — pages can still subscribe but
// the prefix is generic. Keeps the namespace open without a registry.
func subjectTopic(kind, subject string) string {
	switch kind {
	case "stack.up", "stack.down", "stack.restart", "stack.update":
		return "stack:" + subject
	case "cron.run":
		return "cron:" + subject
	}
	return "subject:" + subject
}
