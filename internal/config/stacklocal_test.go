package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadStackLocalConfig_NewPath(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "stackr"), 0o755))

	content := `
remote_repo:
  url: git@github.com:org/app.git
  branch: main
  path: deploy
  release:
    type: tag
    ref: ${APP_VERSION}

compose_files:
  - docker-compose.yml
  - docker-compose.prod.yml

env:
  LOG_LEVEL: debug
  FEATURE_FLAG: "true"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stackr", "config.yaml"), []byte(content), 0o644))

	cfg, err := LoadStackLocalConfig(dir)
	require.NoError(t, err)

	require.True(t, cfg.IsRemote())
	require.Equal(t, "git@github.com:org/app.git", cfg.RemoteRepo.URL)
	require.Equal(t, "main", cfg.RemoteRepo.Branch)
	require.Equal(t, "deploy", cfg.RemoteRepo.Path)
	require.Equal(t, "tag", cfg.RemoteRepo.Release.Type)
	require.Equal(t, "${APP_VERSION}", cfg.RemoteRepo.Release.Ref)

	require.Equal(t, []string{"docker-compose.yml", "docker-compose.prod.yml"}, cfg.ComposeFiles)
	require.Equal(t, "debug", cfg.Env["LOG_LEVEL"])
	require.Equal(t, "true", cfg.Env["FEATURE_FLAG"])
}

func TestLoadStackLocalConfig_NewPathLocalStack(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "stackr"), 0o755))

	content := `
compose_files:
  - docker-compose.yml
  - docker-compose.override.yml

env:
  DEBUG: "1"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stackr", "config.yaml"), []byte(content), 0o644))

	cfg, err := LoadStackLocalConfig(dir)
	require.NoError(t, err)

	require.False(t, cfg.IsRemote())
	require.Nil(t, cfg.RemoteRepo)
	require.Equal(t, []string{"docker-compose.yml", "docker-compose.override.yml"}, cfg.ComposeFiles)
	require.Equal(t, "1", cfg.Env["DEBUG"])
}

func TestLoadStackLocalConfig_LegacyFallback(t *testing.T) {
	dir := t.TempDir()

	content := `
remote_repo:
  url: git@github.com:org/app.git
  release:
    type: tag
    ref: v1.0.0
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stackr-repo.yml"), []byte(content), 0o644))

	cfg, err := LoadStackLocalConfig(dir)
	require.NoError(t, err)

	require.True(t, cfg.IsRemote())
	require.Equal(t, "git@github.com:org/app.git", cfg.RemoteRepo.URL)
	require.Equal(t, "main", cfg.RemoteRepo.Branch) // default
	require.Equal(t, ".", cfg.RemoteRepo.Path)       // default
	require.Equal(t, "tag", cfg.RemoteRepo.Release.Type)
	require.Equal(t, "v1.0.0", cfg.RemoteRepo.Release.Ref)

	// Legacy fallback should use default compose files
	require.Equal(t, []string{"docker-compose.yml"}, cfg.ComposeFiles)
	require.Empty(t, cfg.Env)
}

func TestLoadStackLocalConfig_NeitherFileExists(t *testing.T) {
	dir := t.TempDir()

	cfg, err := LoadStackLocalConfig(dir)
	require.NoError(t, err)

	require.False(t, cfg.IsRemote())
	require.Nil(t, cfg.RemoteRepo)
	require.Equal(t, []string{"docker-compose.yml"}, cfg.ComposeFiles)
	require.Empty(t, cfg.Env)
}

func TestLoadStackLocalConfig_NewPathPriority(t *testing.T) {
	// When both stackr/config.yaml and stackr-repo.yml exist, the new path takes priority
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "stackr"), 0o755))

	newContent := `
compose_files:
  - docker-compose.yml
  - docker-compose.prod.yml
env:
  SOURCE: new
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stackr", "config.yaml"), []byte(newContent), 0o644))

	legacyContent := `
remote_repo:
  url: git@github.com:org/app.git
  release:
    type: tag
    ref: v1.0.0
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stackr-repo.yml"), []byte(legacyContent), 0o644))

	cfg, err := LoadStackLocalConfig(dir)
	require.NoError(t, err)

	// Should use new path, not legacy
	require.False(t, cfg.IsRemote())
	require.Equal(t, "new", cfg.Env["SOURCE"])
	require.Equal(t, []string{"docker-compose.yml", "docker-compose.prod.yml"}, cfg.ComposeFiles)
}

func TestLoadStackLocalConfig_DefaultComposeFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "stackr"), 0o755))

	// Empty config — compose_files should default to ["docker-compose.yml"]
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stackr", "config.yaml"), []byte(""), 0o644))

	cfg, err := LoadStackLocalConfig(dir)
	require.NoError(t, err)

	require.Equal(t, []string{"docker-compose.yml"}, cfg.ComposeFiles)
	require.Empty(t, cfg.Env)
}

func TestLoadStackLocalConfig_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "stackr"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stackr", "config.yaml"), []byte(":::bad"), 0o644))

	_, err := LoadStackLocalConfig(dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to parse")
}

func TestLoadStackLocalConfig_RemoteMissingURL(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "stackr"), 0o755))

	content := `
remote_repo:
  branch: main
  release:
    type: tag
    ref: v1.0.0
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stackr", "config.yaml"), []byte(content), 0o644))

	_, err := LoadStackLocalConfig(dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "remote_repo.url is required")
}

func TestDefaultStackLocalConfig(t *testing.T) {
	cfg := DefaultStackLocalConfig()
	require.Nil(t, cfg.RemoteRepo)
	require.Equal(t, []string{"docker-compose.yml"}, cfg.ComposeFiles)
	require.NotNil(t, cfg.Env)
	require.Empty(t, cfg.Env)
}
