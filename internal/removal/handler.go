package removal

import (
	"context"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/jamestiberiuskirk/stackr/internal/config"
)

// HandlerConfig configures the removal handler
type HandlerConfig struct {
	ContinueOnArchiveError bool
	CleanupTimeout         time.Duration
}

// Handler orchestrates stack removal detection and cleanup
type Handler struct {
	tracker       *Tracker
	archiveConfig ArchiveConfig
	stacksDir     string
	config        HandlerConfig
}

// NewHandler creates a new removal handler
func NewHandler(cfg config.Config, handlerCfg HandlerConfig) *Handler {
	poolBases := make(map[string]string)
	for name, rel := range cfg.Global.Paths.Pools {
		key := strings.ToUpper(strings.TrimSpace(name))
		poolBases[key] = absolutePath(cfg.RepoRoot, rel)
	}

	backupDir := absolutePath(cfg.RepoRoot, cfg.Global.Paths.BackupDir)

	return &Handler{
		tracker: NewTracker(),
		archiveConfig: ArchiveConfig{
			BackupDir: backupDir,
			PoolBases: poolBases,
			StacksDir: cfg.StacksDir,
		},
		stacksDir: cfg.StacksDir,
		config:    handlerCfg,
	}
}

// Initialize sets the initial stack state
func (h *Handler) Initialize(stacks []string) {
	h.tracker.Initialize(stacks)
	log.Printf("initialized removal tracker with %d stacks", len(stacks))
}

// CheckForRemovals scans for removed stacks and handles cleanup
// This is called from the file watcher callback
func (h *Handler) CheckForRemovals(currentStacks []string) {
	removed := h.tracker.Update(currentStacks)

	if len(removed) == 0 {
		return
	}

	log.Printf("detected %d removed stack(s): %v", len(removed), removed)

	for _, stack := range removed {
		h.handleRemovedStack(stack)
	}
}

func (h *Handler) handleRemovedStack(stack string) {
	log.Printf("handling removal of stack: %s", stack)

	// Phase 1: Archive
	archivePath, err := Archive(stack, h.archiveConfig)
	if err != nil {
		log.Printf("ERROR: failed to archive stack %s: %v", stack, err)
		if !h.config.ContinueOnArchiveError {
			log.Printf("skipping cleanup for %s due to archive failure", stack)
			return
		}
		log.Printf("continuing with cleanup despite archive failure (ContinueOnArchiveError=true)")
	} else {
		log.Printf("archived stack %s to %s", stack, archivePath)
	}

	// Phase 2: Cleanup Docker resources
	ctx, cancel := context.WithTimeout(context.Background(), h.config.CleanupTimeout)
	defer cancel()

	if err := Cleanup(ctx, stack, h.stacksDir); err != nil {
		log.Printf("ERROR: failed to cleanup stack %s: %v", stack, err)
		return
	}

	log.Printf("successfully cleaned up stack %s", stack)
}

// absolutePath returns an absolute path, handling both absolute and relative paths
func absolutePath(root, p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return root
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(root, p)
}
