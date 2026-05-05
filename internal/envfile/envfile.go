package envfile

import (
	"fmt"
	"os"
	"strings"
)

type Snapshot struct {
	Data []byte
	Mode os.FileMode
}

func SnapshotFile(path string) (Snapshot, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, err
	}

	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode()
	}

	return Snapshot{Data: content, Mode: mode}, nil
}

func Restore(path string, snap Snapshot) error {
	return os.WriteFile(path, snap.Data, snap.Mode)
}

// Entry is a single KEY=VALUE line in the env file. Comment and blank
// lines are skipped during Read, so consumers see only assignments — the
// UI doesn't need to surface comments today.
type Entry struct {
	Key   string
	Value string
}

// Read parses the env file and returns its entries in file order.
// Comments and blank lines are dropped silently. A missing file returns
// an empty slice without an error so the UI can render an empty table on
// a fresh checkout.
func Read(path string) ([]Entry, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	normalized := strings.ReplaceAll(string(content), "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	out := make([]Entry, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		out = append(out, Entry{
			Key:   strings.TrimSpace(parts[0]),
			Value: parts[1],
		})
	}
	return out, nil
}

// Delete removes the line assigning key from the env file. Returns
// (true, nil) if the key was present and removed, (false, nil) if the
// key wasn't there. Comments and blank lines around the deleted line
// are preserved verbatim.
func Delete(path, key string) (bool, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	normalized := strings.ReplaceAll(string(content), "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	out := make([]string, 0, len(lines))
	removed := false
	for i, line := range lines {
		// Drop the trailing-newline empty line at the end so we don't
		// add a second blank when we re-join.
		if i == len(lines)-1 && line == "" {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 && strings.TrimSpace(parts[0]) == key {
				removed = true
				continue
			}
		}
		out = append(out, line)
	}
	if !removed {
		return false, nil
	}
	updated := strings.Join(out, "\n") + "\n"
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func Update(path, key, value string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	normalized := strings.ReplaceAll(string(content), "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	var updated []string
	var previous string
	replaced := false

	for i, line := range lines {
		if i == len(lines)-1 && line == "" {
			continue
		}

		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			updated = append(updated, line)
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) == key {
			previous = parts[1]
			updated = append(updated, fmt.Sprintf("%s=%s", key, value))
			replaced = true
			continue
		}

		updated = append(updated, line)
	}

	if !replaced {
		if len(updated) > 0 && updated[len(updated)-1] != "" {
			updated = append(updated, "")
		}
		updated = append(updated, fmt.Sprintf("%s=%s", key, value))
	}

	updatedContent := strings.Join(updated, "\n") + "\n"
	if err := os.WriteFile(path, []byte(updatedContent), 0o644); err != nil {
		return "", err
	}

	return strings.TrimSpace(previous), nil
}
