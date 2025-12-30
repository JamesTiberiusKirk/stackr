package removal

import (
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
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

	return copyDir(src, dest)
}

// copyDir recursively copies a directory
func copyDir(src, dest string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)

		info, err := d.Info()
		if err != nil {
			return err
		}

		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode())
		case d.Type()&os.ModeSymlink != 0:
			ref, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(ref, target)
		default:
			return copyFile(path, target, info.Mode())
		}
	})
}

// copyFile copies a single file
func copyFile(src, dest string, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		_ = in.Close()
	}()

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
	}()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}
