package fsutil

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// CopyDir recursively copies a directory tree from src to dest.
func CopyDir(src, dest string) error {
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
			return CopyFile(path, target, info.Mode())
		}
	})
}

// CopyFile copies a single file from src to dest with the given permissions.
func CopyFile(src, dest string, mode fs.FileMode) error {
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
