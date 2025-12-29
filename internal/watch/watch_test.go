package watch

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWatchStacksDebouncesEvents(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var events []string
	done := make(chan struct{})

	require.NoError(t, WatchStacks(ctx, root, func(path string) {
		mu.Lock()
		events = append(events, path)
		mu.Unlock()
		close(done)
	}))

	file := filepath.Join(root, "test.txt")
	require.NoError(t, os.WriteFile(file, []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(file, []byte("b"), 0o644))

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout waiting for watcher event")
	}

	time.Sleep(debounceWindow + 50*time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, events, 1)
	require.NotEmpty(t, events[0])
	cancel()
}
