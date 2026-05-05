package reconcile

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jamestiberiuskirk/stackr/internal/config"
)

func TestPathToStack(t *testing.T) {
	stacksDir := "/repo/stacks"

	tests := []struct {
		name      string
		path      string
		wantStack string
	}{
		{
			name:      "compose file inside stack",
			path:      "/repo/stacks/nginx/docker-compose.yml",
			wantStack: "nginx",
		},
		{
			name:      "nested file inside stack",
			path:      "/repo/stacks/nginx/conf/site.conf",
			wantStack: "nginx",
		},
		{
			name:      "top-level file in stacks dir is not a stack",
			path:      "/repo/stacks/some-orphan.txt",
			wantStack: "",
		},
		{
			name:      "file outside stacks dir",
			path:      "/repo/other/thing.txt",
			wantStack: "",
		},
		{
			name:      "stacks dir itself",
			path:      "/repo/stacks",
			wantStack: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.wantStack, PathToStack(stacksDir, tt.path))
		})
	}
}

func TestPlanForStack(t *testing.T) {
	plan := PlanForStack("nginx", "compose changed")
	require.Len(t, plan.Stacks, 1)
	require.Equal(t, "nginx", plan.Stacks[0].Stack)
	require.Equal(t, "up", plan.Stacks[0].Action,
		"file-change-driven reconciles always use bare 'up' — restarts the stack regardless of image-state, "+
			"matching docker compose's behaviour when the compose file itself changed")
	require.Equal(t, "compose changed", plan.Stacks[0].Reason)
	require.False(t, plan.IsEmpty())
}

func TestPlanForStack_EmptyStackProducesEmptyPlan(t *testing.T) {
	plan := PlanForStack("", "anything")
	require.True(t, plan.IsEmpty(),
		"an empty stack name must produce an empty plan — Apply iterating over zero changes is safer than enqueuing a 'stack=' job")
}

func TestApply_EnqueuesOnePerChange(t *testing.T) {
	type call struct {
		stack, action, trigger string
	}
	var calls []call
	var mu sync.Mutex
	enqueue := func(stack, action, trigger string) string {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, call{stack, action, trigger})
		return "job-" + stack
	}

	r := New(config.Config{}, enqueue)
	plan := Plan{Stacks: []StackChange{
		{Stack: "nginx", Action: "up", Reason: "x"},
		{Stack: "heartbeat", Action: "up", Reason: "x"},
	}}
	ids := r.Apply(plan)

	require.Equal(t, []string{"job-nginx", "job-heartbeat"}, ids)
	require.Len(t, calls, 2)
	require.Equal(t, "watch", calls[0].trigger,
		"trigger must be 'watch' so the audit log distinguishes file-driven reconciles from manual / api / cron")
	require.Equal(t, "up", calls[0].action)
}

func TestHandleEvent_DebouncesPerStack(t *testing.T) {
	// Pinned because the whole point of the debounce is to absorb
	// editor save-and-rename bursts; without it, three rapid events
	// would enqueue three identical jobs.
	tmp := t.TempDir()
	require.NoError(t, makeDirs(tmp, "stacks/nginx"))

	cfg := config.Config{StacksDir: filepath.Join(tmp, "stacks")}
	var counter int32
	r := New(cfg, func(_, _, _ string) string {
		atomic.AddInt32(&counter, 1)
		return ""
	})

	for i := 0; i < 5; i++ {
		r.HandleEvent(filepath.Join(cfg.StacksDir, "nginx", "docker-compose.yml"))
	}

	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&counter) == 1
	}, 2*time.Second, 10*time.Millisecond)

	// Make sure no extra fires arrive after settle.
	time.Sleep(debounceWindow + 100*time.Millisecond)
	require.Equal(t, int32(1), atomic.LoadInt32(&counter),
		"five events for one stack must collapse to a single enqueue")
}

func TestHandleEvent_IgnoresPathsOutsideStacksDir(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.Config{StacksDir: filepath.Join(tmp, "stacks")}
	var fired int32
	r := New(cfg, func(_, _, _ string) string {
		atomic.AddInt32(&fired, 1)
		return ""
	})

	r.HandleEvent(filepath.Join(tmp, "other-place", "compose.yml"))

	time.Sleep(debounceWindow + 100*time.Millisecond)
	require.Equal(t, int32(0), atomic.LoadInt32(&fired),
		"events outside the stacks dir must never enqueue work — the watcher might see top-level repo edits we don't care about")
}

// makeDirs is a tiny test helper to set up a stacks-tree without
// dragging in a fixture package for one path.
func makeDirs(root string, rels ...string) error {
	for _, r := range rels {
		if err := osMkdirAll(filepath.Join(root, r)); err != nil {
			return err
		}
	}
	return nil
}
