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
