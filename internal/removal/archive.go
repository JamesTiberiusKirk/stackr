package removal

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jamestiberiuskirk/stackr/internal/fsutil"
)

// ArchiveConfig holds configuration for archiving
type ArchiveConfig struct {
	BackupDir string
	PoolBases map[string]string
	StacksDir string
}

// Archive creates a timestamped archive of a stack's volumes
// Returns archive path and error
func Archive(stack string, cfg ArchiveConfig) (string, error) {
	timestamp := time.Now().Format("20060102_150405")
	archivePath := filepath.Join(cfg.BackupDir, "archives", fmt.Sprintf("%s-%s", stack, timestamp))

	if err := os.MkdirAll(archivePath, 0o755); err != nil {
		return "", fmt.Errorf("failed to create archive directory: %w", err)
	}

	log.Printf("archiving removed stack %s to %s", stack, archivePath)

	stackDir := filepath.Join(cfg.StacksDir, stack)

	// Archive config directories (if they still exist)
	configDirs := []struct {
		src string
		dst string
	}{
		{filepath.Join(stackDir, "config"), filepath.Join(archivePath, "config")},
		{filepath.Join(stackDir, "dashboards"), filepath.Join(archivePath, "dashboards")},
		{filepath.Join(stackDir, "dynamic"), filepath.Join(archivePath, "dynamic")},
	}

	for _, item := range configDirs {
		if err := copyDirIfExists(item.src, item.dst); err != nil {
			return archivePath, fmt.Errorf("failed to archive %s: %w", item.src, err)
		}
	}

	// Archive pool volumes (even if stack directory is gone)
	for poolName, poolBase := range cfg.PoolBases {
		poolVolume := filepath.Join(poolBase, stack)
		poolDest := filepath.Join(archivePath, fmt.Sprintf("pool_%s", strings.ToLower(poolName)))
		if err := copyDirIfExists(poolVolume, poolDest); err != nil {
			return archivePath, fmt.Errorf("failed to archive pool %s: %w", poolName, err)
		}
	}

	log.Printf("successfully archived stack %s", stack)
	return archivePath, nil
}

// copyDirIfExists copies a directory if it exists, skips if not
func copyDirIfExists(src, dest string) error {
	info, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Skip if source doesn't exist
		}
		return err
	}

	if !info.IsDir() {
		return nil
	}

	return fsutil.CopyDir(src, dest)
}
