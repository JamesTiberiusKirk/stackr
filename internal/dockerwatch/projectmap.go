package dockerwatch

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/jamestiberiuskirk/stackr/internal/config"
	"github.com/jamestiberiuskirk/stackr/internal/stackcmd"
)

// composeName is the minimal yaml shape we care about — only the
// top-level `name:` field. Avoids dragging the full compose schema in
// when we just need this one field.
type composeName struct {
	Name string `yaml:"name"`
}

// BuildProjectMap walks the discovered stacks and maps each one's
// docker-compose project name back to the stack name. Used by the
// dockerwatch.Watcher to translate /events stream events into the
// right hub topic.
//
// Errors on individual stacks are non-fatal — a missing or malformed
// compose file logs and skips that stack rather than failing boot. The
// daemon stays up; the unwatched stack just won't get live status
// updates until the file is fixed and the daemon restarts.
func BuildProjectMap(cfg config.Config) (map[string]string, error) {
	stacks, err := stackcmd.DiscoverStacks(cfg)
	if err != nil {
		return nil, fmt.Errorf("discover stacks: %w", err)
	}
	out := make(map[string]string, len(stacks))
	for _, s := range stacks {
		composePath := s.PrimaryComposePath()
		if composePath == "" {
			continue
		}
		project := readProjectName(composePath, s.Name)
		if project == "" {
			continue
		}
		out[project] = s.Name
	}
	return out, nil
}

// readProjectName parses just the `name:` field from a compose file.
// If absent or unreadable, fall back to the normalised directory name
// (matches docker compose's default project derivation).
func readProjectName(composePath, dirName string) string {
	data, err := os.ReadFile(composePath)
	if err != nil {
		// Fall back to dir-derived name; the watcher might be
		// initialised before the file becomes readable.
		return projectFromName("", filepath.Base(filepath.Dir(composePath)))
	}
	var parsed composeName
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return projectFromName("", dirName)
	}
	return projectFromName(parsed.Name, dirName)
}
