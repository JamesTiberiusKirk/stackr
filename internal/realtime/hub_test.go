package realtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jamestiberiuskirk/stackr/internal/jobs"
)

func TestSubscribe_DeliversMatchingEvents(t *testing.T) {
	h := New()
	_, ch := h.Subscribe("stack:nginx")

	h.Publish(Event{Topic: "stack:nginx", Type: "x", Data: 1})
	h.Publish(Event{Topic: "stack:other", Type: "x", Data: 2}) // shouldn't match

	select {
	case e := <-ch:
		require.Equal(t, "stack:nginx", e.Topic)
		require.Equal(t, 1, e.Data)
	case <-time.After(time.Second):
		t.Fatal("expected to receive an event")
	}

	// No second event — the unrelated topic must not leak.
	select {
	case e := <-ch:
		t.Fatalf("unexpected event: %+v", e)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestUnsubscribe_ClosesChannelAndStopsDelivery(t *testing.T) {
	h := New()
	id, ch := h.Subscribe("topic")
	h.Unsubscribe(id)
	_, ok := <-ch
	require.False(t, ok, "channel must close on unsubscribe so range loops in WS handlers exit")

	// Idempotent — second call is a no-op (no panic on the closed
	// channel).
	require.NotPanics(t, func() { h.Unsubscribe(id) })
}

func TestPublish_SlowSubscriberDoesNotBlockOthers(t *testing.T) {
	// Pinned because back-pressure on the publisher would mean a hung
	// browser tab freezes deploys for everyone else. The slow sub gets
	// dropped events; healthy subs keep receiving.
	h := New()
	_, slow := h.Subscribe("topic")
	_, fast := h.Subscribe("topic")

	// Fill the slow subscriber past its buffer.
	for i := 0; i < subscriberBuffer+10; i++ {
		h.Publish(Event{Topic: "topic", Type: "tick", Data: i})
	}

	// Drain a few events from fast — it must have received them despite
	// slow being saturated.
	got := 0
	timeout := time.After(time.Second)
loop:
	for got < 3 {
		select {
		case <-fast:
			got++
		case <-timeout:
			break loop
		}
	}
	require.GreaterOrEqual(t, got, 3,
		"fast subscriber must receive events even when a co-subscriber is full")
	_ = slow // avoid the no-receive-warning
}

func TestPublishJobEvent_FansOutToJobAndSubjectTopics(t *testing.T) {
	// Pinned: WS clients on /stacks/nginx subscribe to stack:nginx, ones
	// tracking a single deploy subscribe to job:<id>. Both must hear.
	h := New()
	_, jobCh := h.Subscribe("job:abc")
	_, stackCh := h.Subscribe("stack:nginx")

	h.PublishJobEvent(jobs.Event{
		JobID: "abc", Kind: "stack.up", Subject: "nginx", Status: "success",
	})

	select {
	case e := <-jobCh:
		require.Equal(t, "job.success", e.Type)
	case <-time.After(time.Second):
		t.Fatal("job-topic subscriber missed the event")
	}
	select {
	case e := <-stackCh:
		require.Equal(t, "job.success", e.Type)
	case <-time.After(time.Second):
		t.Fatal("stack-topic subscriber missed the event")
	}
}

func TestAddTopic_LetsClientFollowAJobAfterSubscribing(t *testing.T) {
	h := New()
	id, ch := h.Subscribe("stack:nginx")
	h.AddTopic(id, "job:later")

	h.Publish(Event{Topic: "job:later", Type: "x"})
	select {
	case e := <-ch:
		require.Equal(t, "job:later", e.Topic)
	case <-time.After(time.Second):
		t.Fatal("AddTopic-added topic must reach the existing subscription — that's how WS clients track a freshly-enqueued job")
	}
}
