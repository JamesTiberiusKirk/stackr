package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoad_UsesGlobalConfigAndEnvOverrides(t *testing.T) {
	t.Helper()
	repo := t.TempDir()
	require := require.New(t)

global := `
stacks_dir: custom-stacks
cron:
  profile: nightly
http:
  base_domain: example.local
`
	require.NoError(os.WriteFile(filepath.Join(repo, ".stackr.yaml"), []byte(global), 0o644))

	customStacks := filepath.Join(repo, "custom-stacks")
	require.NoError(os.MkdirAll(customStacks, 0o755))

	t.Setenv("STACKR_TOKEN", "dummy-token")

	cfg, err := Load(repo)
	require.NoError(err)

	require.Equal(customStacks, cfg.StacksDir)
require.Equal("nightly", cfg.Global.Cron.DefaultProfile)
require.Equal("example.local", cfg.Global.HTTP.BaseDomain)

	expectedPath := filepath.Join(repo, ".stackr.yaml")
	require.Equal(expectedPath, cfg.Global.Path)
}

func TestLoad_AppliesEnvOverrideForStacksDir(t *testing.T) {
	t.Helper()
	repo := t.TempDir()
	stackOverride := filepath.Join(t.TempDir(), "stacks")
	require.NoError(t, os.MkdirAll(stackOverride, 0o755))

	t.Setenv("STACKR_STACKS_DIR", stackOverride)
	t.Setenv("STACKR_TOKEN", "env-token")

	cfg, err := Load(repo)
	require := require.New(t)
	require.NoError(err)
	require.Equal(stackOverride, cfg.StacksDir)
}

func TestLoad_UsesDefaultsWhenConfigMissing(t *testing.T) {
	t.Helper()
	repo := t.TempDir()
	defaultStacks := filepath.Join(repo, "stacks")
	require.NoError(t, os.MkdirAll(defaultStacks, 0o755))

	t.Setenv("STACKR_TOKEN", "token")

	cfg, err := Load(repo)
	require := require.New(t)
	require.NoError(err)
	require.Equal(defaultStacks, cfg.StacksDir)
	require.Equal("cron", cfg.Global.Cron.DefaultProfile)
}

