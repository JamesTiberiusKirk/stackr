package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// StackLocalConfig is the unified per-stack config loaded from stackr/config.yaml.
// It replaces the standalone stackr-repo.yml and adds compose_files + env overrides.
type StackLocalConfig struct {
	RemoteRepo   *RemoteStackConfig `yaml:"remote_repo"`
	ComposeFiles []string           `yaml:"compose_files"`
	Env          map[string]string  `yaml:"env"`
}

// DefaultStackLocalConfig returns a StackLocalConfig with sensible defaults:
// no remote, single docker-compose.yml, no env overrides.
func DefaultStackLocalConfig() StackLocalConfig {
	return StackLocalConfig{
		ComposeFiles: []string{"docker-compose.yml"},
		Env:          map[string]string{},
	}
}

// LoadStackLocalConfig loads the per-stack config from stackr/config.yaml inside the
// given stack directory. If that file does not exist it falls back to the legacy
// stackr-repo.yml. When neither file exists the returned config uses defaults
// (local stack, single docker-compose.yml, no env overrides).
func LoadStackLocalConfig(stackDir string) (*StackLocalConfig, error) {
	// Try new path first: stackr/config.yaml
	newPath := filepath.Join(stackDir, "stackr", "config.yaml")
	if data, err := os.ReadFile(newPath); err == nil {
		return parseStackLocalConfig(data, newPath)
	}

	// Fallback to legacy stackr-repo.yml
	legacyPath := filepath.Join(stackDir, "stackr-repo.yml")
	if data, err := os.ReadFile(legacyPath); err == nil {
		return parseLegacyConfig(data, legacyPath)
	}

	// Neither file exists — return defaults (local stack)
	cfg := DefaultStackLocalConfig()
	return &cfg, nil
}

func parseStackLocalConfig(data []byte, path string) (*StackLocalConfig, error) {
	var cfg StackLocalConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}

	if cfg.RemoteRepo != nil {
		if err := validateRemoteRepo(cfg.RemoteRepo); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		applyRemoteDefaults(cfg.RemoteRepo)
	}

	if len(cfg.ComposeFiles) == 0 {
		cfg.ComposeFiles = []string{"docker-compose.yml"}
	}
	if cfg.Env == nil {
		cfg.Env = map[string]string{}
	}

	return &cfg, nil
}

func parseLegacyConfig(data []byte, path string) (*StackLocalConfig, error) {
	var def RemoteStackDefinition
	if err := yaml.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}

	if err := validateRemoteRepo(&def.RemoteRepo); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	applyRemoteDefaults(&def.RemoteRepo)

	return &StackLocalConfig{
		RemoteRepo:   &def.RemoteRepo,
		ComposeFiles: []string{"docker-compose.yml"},
		Env:          map[string]string{},
	}, nil
}

func validateRemoteRepo(r *RemoteStackConfig) error {
	if r.URL == "" {
		return fmt.Errorf("remote_repo.url is required")
	}
	if r.Release.Type == "" {
		return fmt.Errorf("remote_repo.release.type is required (must be 'tag' or 'commit')")
	}
	if r.Release.Type != "tag" && r.Release.Type != "commit" {
		return fmt.Errorf("remote_repo.release.type must be 'tag' or 'commit', got: %s", r.Release.Type)
	}
	if r.Release.Ref == "" {
		return fmt.Errorf("remote_repo.release.ref is required")
	}
	return nil
}

func applyRemoteDefaults(r *RemoteStackConfig) {
	if r.Branch == "" {
		r.Branch = "main"
	}
	if r.Path == "" {
		r.Path = "."
	}
}

// IsRemote returns true if this config defines a remote stack.
func (c *StackLocalConfig) IsRemote() bool {
	return c.RemoteRepo != nil
}
