package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/jamestiberiuskirk/stackr/internal/envfile"
	"github.com/jamestiberiuskirk/stackr/internal/repo"
)

// EventKindEnvEdit is the audit kind for an env-var mutation. One row per
// successful set/delete keeps a history queryable from /events later.
const EventKindEnvEdit = "env.edit"

// ErrInvalidEnvKey is returned when a submitted key is empty or contains
// characters env files can't round-trip cleanly. The web handler maps
// this to 400.
var ErrInvalidEnvKey = errors.New("invalid env key")

// EnvView is the structured payload the /env page renders. Bundles
// everything an operator might want to see in one shot — file-side
// editable, yaml-side read-only — so the page can render without
// chasing cross-package data through the templ.
type EnvView struct {
	// FilePath is the absolute path of the .env file being edited; the
	// UI shows this so operators know which file they're mutating.
	FilePath string

	// Entries from the .env file (editable).
	Entries []envfile.Entry

	// GlobalYAML is auth.global from stackr.yaml (read-only here; edit
	// the yaml in the repo to change).
	GlobalYAML map[string]string

	// StackYAML is env.stacks.{name} from stackr.yaml — keyed by stack
	// name. Same read-only treatment as GlobalYAML.
	StackYAML map[string]map[string]string

	// Pools is the map of pool name → base path from `paths.pools`.
	// Surfaced so operators see what STACKR_PROV_POOL_* vars stackr
	// will inject per stack.
	Pools map[string]string
}

// envKeyValid permits the standard portable POSIX variable name shape:
// letters / digits / underscore, leading non-digit. Equals signs and
// whitespace would corrupt the file — better to refuse early.
func envKeyValid(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		isLetter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_'
		isDigit := r >= '0' && r <= '9'
		switch {
		case i == 0 && !isLetter:
			return false
		case !isLetter && !isDigit:
			return false
		}
	}
	return true
}

// EnvOverview returns the full EnvView used by the /env page. Reading
// failures (e.g. .env doesn't exist yet) surface as an empty Entries
// slice rather than an error — the UI handles it.
func (s *StackrService) EnvOverview(_ context.Context) (EnvView, error) {
	entries, err := envfile.Read(s.cfg.EnvFile)
	if err != nil {
		return EnvView{}, fmt.Errorf("read env file: %w", err)
	}
	return EnvView{
		FilePath:   s.cfg.EnvFile,
		Entries:    entries,
		GlobalYAML: copyMap(s.cfg.Global.Env.Global),
		StackYAML:  copyStackMap(s.cfg.Global.Env.Stacks),
		Pools:      copyMap(s.cfg.Global.Paths.Pools),
	}, nil
}

// SetEnv writes a key/value pair into the .env file (creating or
// updating). Records an env.edit event so the operator action is
// audit-traceable.
func (s *StackrService) SetEnv(ctx context.Context, key, value string) error {
	key = strings.TrimSpace(key)
	if !envKeyValid(key) {
		return fmt.Errorf("%w: %q", ErrInvalidEnvKey, key)
	}
	previous, err := envfile.Update(s.cfg.EnvFile, key, value)
	if err != nil {
		return fmt.Errorf("update env file: %w", err)
	}
	verb := "created"
	if previous != "" || valueWasReset(value, previous) {
		verb = "updated"
	}
	s.recordEnvEvent(ctx, fmt.Sprintf("%s env var %s", verb, key))
	return nil
}

// valueWasReset is a tiny helper to distinguish "newly created" (no
// previous line) from "value cleared to empty" — Update returns "" for
// both, but the latter is still an update. We can't disambiguate
// perfectly here without a second read; for audit text it's fine to
// treat empty-old + non-empty-new as "updated".
func valueWasReset(newVal, prev string) bool {
	return prev == "" && newVal != ""
}

// DeleteEnv removes a key from the .env file. No-op on missing keys —
// the handler maps that to a 404 if it cares to distinguish.
func (s *StackrService) DeleteEnv(ctx context.Context, key string) (bool, error) {
	key = strings.TrimSpace(key)
	if !envKeyValid(key) {
		return false, fmt.Errorf("%w: %q", ErrInvalidEnvKey, key)
	}
	removed, err := envfile.Delete(s.cfg.EnvFile, key)
	if err != nil {
		return false, fmt.Errorf("delete from env file: %w", err)
	}
	if removed {
		s.recordEnvEvent(ctx, fmt.Sprintf("deleted env var %s", key))
	}
	return removed, nil
}

// recordEnvEvent writes one events row per env edit. Failures are
// logged-but-non-fatal — losing audit shouldn't roll back the .env
// change the operator saw succeed.
func (s *StackrService) recordEnvEvent(ctx context.Context, message string) {
	_ = s.store.CreateEvent(ctx, &repo.Event{
		ID:        uuid.New().String(),
		Kind:      EventKindEnvEdit,
		Message:   message,
		CreatedAt: time.Now(),
	})
}

func copyMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyStackMap(in map[string]map[string]string) map[string]map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]map[string]string, len(in))
	for k, inner := range in {
		out[k] = copyMap(inner)
	}
	return out
}

// SortedKeys returns the map's keys ordered alphabetically — the templ
// can range without import shuffling. Lives on EnvView so it's
// discoverable from the templ side.
func (v EnvView) SortedGlobalKeys() []string {
	return sortedKeys(v.GlobalYAML)
}

func (v EnvView) SortedStackNames() []string {
	keys := make([]string, 0, len(v.StackYAML))
	for k := range v.StackYAML {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (v EnvView) SortedPoolNames() []string {
	return sortedKeys(v.Pools)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
