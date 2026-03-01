package envfile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSnapshotAndRestore(t *testing.T) {
	t.Run("HappyPath", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".env")
		original := "KEY=value\nOTHER=stuff\n"
		require.NoError(t, os.WriteFile(path, []byte(original), 0o644))

		snap, err := SnapshotFile(path)
		require.NoError(t, err)
		require.Equal(t, original, string(snap.Data))

		// Modify the file
		require.NoError(t, os.WriteFile(path, []byte("KEY=changed\n"), 0o644))

		// Restore
		require.NoError(t, Restore(path, snap))

		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, original, string(data))
	})

	t.Run("PreservesPermissions", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".env")
		require.NoError(t, os.WriteFile(path, []byte("SECRET=val\n"), 0o600))

		snap, err := SnapshotFile(path)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), snap.Mode.Perm())

		// Overwrite with different perms
		require.NoError(t, os.WriteFile(path, []byte("changed"), 0o644))

		// Restore should bring back 0600
		require.NoError(t, Restore(path, snap))

		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	})

	t.Run("FileNotFound", func(t *testing.T) {
		_, err := SnapshotFile("/nonexistent/path/.env")
		require.Error(t, err)
	})
}

func TestUpdate(t *testing.T) {
	t.Run("ReplacesExistingKey", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".env")
		require.NoError(t, os.WriteFile(path, []byte("KEY=old\n"), 0o644))

		prev, err := Update(path, "KEY", "new")
		require.NoError(t, err)
		require.Equal(t, "old", prev)

		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Contains(t, string(data), "KEY=new")
		require.NotContains(t, string(data), "KEY=old")
	})

	t.Run("AppendsNewKey", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".env")
		require.NoError(t, os.WriteFile(path, []byte("OTHER=val\n"), 0o644))

		prev, err := Update(path, "KEY", "new")
		require.NoError(t, err)
		require.Equal(t, "", prev)

		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Contains(t, string(data), "OTHER=val")
		require.Contains(t, string(data), "KEY=new")
	})

	t.Run("PreservesComments", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".env")
		content := "# This is a comment\nKEY=old\n\n# Another comment\nOTHER=val\n"
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

		_, err := Update(path, "KEY", "new")
		require.NoError(t, err)

		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Contains(t, string(data), "# This is a comment")
		require.Contains(t, string(data), "# Another comment")
		require.Contains(t, string(data), "KEY=new")
		require.Contains(t, string(data), "OTHER=val")
	})

	t.Run("HandlesEqualsInValue", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".env")
		require.NoError(t, os.WriteFile(path, []byte("KEY=val=with=equals\n"), 0o644))

		prev, err := Update(path, "KEY", "new=value")
		require.NoError(t, err)
		require.Equal(t, "val=with=equals", prev)

		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Contains(t, string(data), "KEY=new=value")
	})

	t.Run("NormalizesCRLF", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".env")
		require.NoError(t, os.WriteFile(path, []byte("KEY=old\r\nOTHER=val\r\n"), 0o644))

		_, err := Update(path, "KEY", "new")
		require.NoError(t, err)

		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NotContains(t, string(data), "\r\n")
		require.Contains(t, string(data), "KEY=new")
		require.Contains(t, string(data), "OTHER=val")
	})

	t.Run("EmptyFile", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".env")
		require.NoError(t, os.WriteFile(path, []byte(""), 0o644))

		_, err := Update(path, "KEY", "value")
		require.NoError(t, err)

		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Contains(t, string(data), "KEY=value")
	})

	t.Run("TrailingNewline", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".env")
		require.NoError(t, os.WriteFile(path, []byte("KEY=val\n"), 0o644))

		_, err := Update(path, "KEY", "new")
		require.NoError(t, err)

		data, err := os.ReadFile(path)
		require.NoError(t, err)
		content := string(data)
		require.True(t, len(content) > 0 && content[len(content)-1] == '\n',
			"file should end with newline")
	})
}
