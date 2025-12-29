package watch

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

const debounceWindow = 2 * time.Second

// WatchStacks monitors root (recursively) for any filesystem changes and invokes cb after
// debouncing bursts of events. The watcher stops when ctx is canceled.
func WatchStacks(ctx context.Context, root string, cb func(string)) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	if err := addRecursive(watcher, root); err != nil {
		_ = watcher.Close()
		return err
	}

	go run(ctx, watcher, cb)
	return nil
}

func run(ctx context.Context, watcher *fsnotify.Watcher, cb func(string)) {
	defer func() {
		_ = watcher.Close()
	}()

	timer := time.NewTimer(debounceWindow)
	if !timer.Stop() {
		<-timer.C
	}
	var (
		pending bool
		last    string
	)

	trigger := func(path string) {
		last = path
		if !pending {
			pending = true
			timer.Reset(debounceWindow)
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(debounceWindow)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Write|fsnotify.Rename) == 0 {
				continue
			}

			if event.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					if err := addRecursive(watcher, event.Name); err != nil {
						log.Printf("failed to add new directory to watcher (%s): %v", event.Name, err)
					}
				}
			}

			trigger(event.Name)
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("stack watcher error: %v", err)
		case <-timer.C:
			if !pending {
				continue
			}
			pending = false
			cb(last)
		}
	}
}

func addRecursive(watcher *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			return nil
		}

		if err := watcher.Add(path); err != nil {
			return err
		}
		return nil
	})
}
