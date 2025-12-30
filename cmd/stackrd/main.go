package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jamestiberiuskirk/stackr/internal/config"
	"github.com/jamestiberiuskirk/stackr/internal/cronjobs"
	"github.com/jamestiberiuskirk/stackr/internal/httpapi"
	"github.com/jamestiberiuskirk/stackr/internal/removal"
	"github.com/jamestiberiuskirk/stackr/internal/runner"
	"github.com/jamestiberiuskirk/stackr/internal/watch"
)

const (
	shutdownTimeout = 30 * time.Second

	defaultRepoRoot = "/srv/stackr_repo"
)

func main() {
	// we should slog +context logging across the entire server here
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)

	repoRoot := os.Getenv("STACKR_REPO_ROOT")
	if repoRoot == "" {
		repoRoot = defaultRepoRoot
	}

	repoRoot, err := config.ResolveRepoRoot(repoRoot)
	if err != nil {
		log.Fatalf("failed to determine repo root: %v", err)
	}

	if strings.TrimSpace(os.Getenv("STACKR_REPO_ROOT")) != "" {
		log.Printf("using repo root override: %s", repoRoot)
	}

	cfg, err := config.Load(repoRoot)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	run := runner.New(cfg)
	handler := httpapi.New(cfg, run)

	scheduler, err := cronjobs.New(cfg)
	if err != nil {
		log.Fatalf("failed to initialize cron scheduler: %v", err)
	}

	if err := scheduler.Start(); err != nil {
		log.Fatalf("failed to start cron scheduler: %v", err)
	}

	// Initialize removal handler
	removalHandler := removal.NewHandler(cfg, removal.HandlerConfig{
		ContinueOnArchiveError: true,
		CleanupTimeout:         5 * time.Minute,
	})

	// Get initial stack list and initialize tracker
	initialStacks, err := loadStackNames(cfg.StacksDir)
	if err != nil {
		log.Printf("warning: failed to load initial stack list: %v", err)
	} else {
		removalHandler.Initialize(initialStacks)
	}

	var watchCancel context.CancelFunc
	{
		var watchCtx context.Context
		watchCtx, watchCancel = context.WithCancel(context.Background())
		if err := watch.WatchStacks(watchCtx, cfg.StacksDir, func(path string) {
			log.Printf("stack change detected (%s), checking for changes", path)

			// Load current stack state
			currentStacks, err := loadStackNames(cfg.StacksDir)
			if err != nil {
				log.Printf("failed to load current stacks: %v", err)
				return
			}

			// Check for removals BEFORE reloading cron (important for cleanup ordering)
			removalHandler.CheckForRemovals(currentStacks)

			// Then reload cron jobs
			if err := scheduler.Reload(); err != nil {
				log.Printf("failed to reload cron scheduler: %v", err)
			}
		}); err != nil {
			log.Printf("stack watcher disabled: %v", err)
			watchCancel()
			watchCancel = nil
		}
	}

	server := &http.Server{
		Addr:              fmt.Sprintf("%s:%s", cfg.Host, cfg.Port),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Minute,
		IdleTimeout:       time.Minute,
	}

	log.Printf("Stackr listening on %s:%s (stacks dir: %s)", cfg.Host, cfg.Port, cfg.StacksDir)

	errCh := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		log.Fatalf("server error: %v", err)
	case sig := <-sigCh:
		log.Printf("signal received (%s), shutting down", sig)
	}

	if watchCancel != nil {
		watchCancel()
	}

	if scheduler != nil {
		scheduler.Stop()
	}

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("server shutdown error: %v", err)
	}

	log.Printf("server stopped gracefully")
}

// loadStackNames scans the stacks directory and returns the names of all valid stacks
func loadStackNames(stacksDir string) ([]string, error) {
	entries, err := os.ReadDir(stacksDir)
	if err != nil {
		return nil, err
	}

	var stacks []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		composePath := filepath.Join(stacksDir, entry.Name(), "docker-compose.yml")
		if _, err := os.Stat(composePath); err == nil {
			stacks = append(stacks, entry.Name())
		}
	}
	return stacks, nil
}
