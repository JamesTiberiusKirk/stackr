package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// RemoteStackConfig defines a remote Git repository for a stack
type RemoteStackConfig struct {
	URL     string        `yaml:"url"`
	Branch  string        `yaml:"branch"`
	Path    string        `yaml:"path"`    // Subdirectory within repo (optional)
	Release ReleaseConfig `yaml:"release"`
}

// ReleaseConfig defines how to resolve the version to deploy
type ReleaseConfig struct {
	Type string `yaml:"type"` // "tag" or "commit"
	Ref  string `yaml:"ref"`  // Can contain ${VAR} references
}

// RemoteStackDefinition is the content of stacks/{name}/stackr.yaml
type RemoteStackDefinition struct {
	RemoteRepo RemoteStackConfig `yaml:"remote_repo"`
}

// DeploymentConfig is the content of .stackr-deployment.yaml in remote repo
type DeploymentConfig struct {
	Env map[string]string `yaml:"env"`
}

var envVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// LoadRemoteStackDefinition reads stacks/{name}/stackr-repo.yml
func LoadRemoteStackDefinition(stacksDir, stackName string) (*RemoteStackDefinition, error) {
	path := filepath.Join(stacksDir, stackName, "stackr-repo.yml")
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read stackr-repo.yml: %w", err)
	}

	var def RemoteStackDefinition
	if err := yaml.Unmarshal(content, &def); err != nil {
		return nil, fmt.Errorf("failed to parse stackr-repo.yml: %w", err)
	}

	// Validate required fields
	if def.RemoteRepo.URL == "" {
		return nil, fmt.Errorf("remote_repo.url is required")
	}
	if def.RemoteRepo.Release.Type == "" {
		return nil, fmt.Errorf("remote_repo.release.type is required (must be 'tag' or 'commit')")
	}
	if def.RemoteRepo.Release.Type != "tag" && def.RemoteRepo.Release.Type != "commit" {
		return nil, fmt.Errorf("remote_repo.release.type must be 'tag' or 'commit', got: %s", def.RemoteRepo.Release.Type)
	}
	if def.RemoteRepo.Release.Ref == "" {
		return nil, fmt.Errorf("remote_repo.release.ref is required")
	}

	// Set defaults
	if def.RemoteRepo.Branch == "" {
		def.RemoteRepo.Branch = "main"
	}
	if def.RemoteRepo.Path == "" {
		def.RemoteRepo.Path = "."
	}

	return &def, nil
}

// LoadDeploymentConfig reads .stackr-deployment.yaml from remote repo
func LoadDeploymentConfig(remoteRepoPath string) (*DeploymentConfig, error) {
	path := filepath.Join(remoteRepoPath, ".stackr-deployment.yaml")

	// This file is optional
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Return empty config if file doesn't exist
			return &DeploymentConfig{Env: make(map[string]string)}, nil
		}
		return nil, fmt.Errorf("failed to read .stackr-deployment.yaml: %w", err)
	}

	var cfg DeploymentConfig
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse .stackr-deployment.yaml: %w", err)
	}

	if cfg.Env == nil {
		cfg.Env = make(map[string]string)
	}

	return &cfg, nil
}

// ResolveVersionRef resolves ${VAR} references in version ref
func ResolveVersionRef(ref string, envVars map[string]string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("version ref is empty")
	}

	// Find all ${VAR} patterns
	matches := envVarPattern.FindAllStringSubmatch(ref, -1)
	if len(matches) == 0 {
		// No variables to resolve, return as-is
		return ref, nil
	}

	resolved := ref
	for _, match := range matches {
		fullMatch := match[0]  // e.g., "${MYAPP_VERSION}"
		varName := match[1]    // e.g., "MYAPP_VERSION"

		value, ok := envVars[varName]
		if !ok {
			return "", fmt.Errorf("environment variable %s (from ref %q) is not defined", varName, ref)
		}

		resolved = strings.ReplaceAll(resolved, fullMatch, value)
	}

	return strings.TrimSpace(resolved), nil
}
