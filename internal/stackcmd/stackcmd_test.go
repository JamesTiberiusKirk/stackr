package stackcmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jamestiberiuskirk/stackr/internal/config"
)

func TestManagerDryRunInvokesDockerConfig(t *testing.T) {
	root := t.TempDir()
	makeDirs(t, root, "stacks/demo")
	writeFile(t, filepath.Join(root, ".env"), envContent(`
COMPOSE_DIRECTORY=stacks
BACKUP_DIR=backups
MY_VAR=value
`))
	writeFile(t, filepath.Join(root, "stacks/demo/docker-compose.yml"), `
services:
  web:
    image: nginx:${MY_VAR}
    volumes:
      - ${STACKR_PROV_POOL_HDD}:/data
`)

	cfg := config.Config{
		RepoRoot:  root,
		EnvFile:   filepath.Join(root, ".env"),
		StacksDir: filepath.Join(root, "stacks"),
		Global:    testGlobalConfig(),
	}

	logPath, cleanup := stubDocker(t)
	defer cleanup()

	manager, err := NewManager(cfg)
	require.NoError(t, err)

	opts := Options{Stacks: []string{"demo"}, DryRun: true}
	require.NoError(t, manager.Run(context.Background(), opts))

	logData, err := os.ReadFile(logPath)
	require.NoError(t, err)
	got := strings.TrimSpace(string(logData))
	require.Contains(t, got, "compose -f")
	require.True(t, strings.HasSuffix(got, "config"), "expected compose config call, got %q", got)
}

func TestManagerGetVarsAppendsMissingEnv(t *testing.T) {
	root := t.TempDir()
	makeDirs(t, root, "stacks/example")
	writeFile(t, filepath.Join(root, ".env"), envContent(`
COMPOSE_DIRECTORY=stacks
`))
	writeFile(t, filepath.Join(root, "stacks/example/docker-compose.yml"), `
services:
  job:
    image: busybox:${IMAGE_TAG}
`)

	cfg := config.Config{
		RepoRoot:  root,
		EnvFile:   filepath.Join(root, ".env"),
		StacksDir: filepath.Join(root, "stacks"),
		Global:    testGlobalConfig(),
	}

	manager, err := NewManager(cfg)
	require.NoError(t, err)

	opts := Options{Stacks: []string{"example"}, GetVars: true}
	require.NoError(t, manager.Run(context.Background(), opts))

	data, err := os.ReadFile(cfg.EnvFile)
	require.NoError(t, err)
	content := string(data)
	require.Contains(t, content, "###### example vars #####")
	require.Contains(t, content, "IMAGE_TAG=")
}

func stubDocker(t *testing.T) (string, func()) {
	t.Helper()
	binDir := t.TempDir()
	logPath := filepath.Join(binDir, "docker.log")
	script := filepath.Join(binDir, "docker")
	writeFile(t, script, "#!/bin/sh\necho \"$@\" >> \""+logPath+"\"\n")
	require.NoError(t, os.Chmod(script, 0o755))

	path := binDir + string(os.PathListSeparator) + os.Getenv("PATH")
	t.Setenv("PATH", path)

	return logPath, func() {
		_ = os.Remove(logPath)
	}
}

func makeDirs(t *testing.T, base string, rel string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(base, rel), 0o755))
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func envContent(body string) string {
	return strings.TrimSpace(body) + "\n"
}

func TestBuildStackEnv(t *testing.T) {
	root := t.TempDir()
	makeDirs(t, root, "stacks/demo")
	writeFile(t, filepath.Join(root, ".env"), "")

	cfg := config.Config{
		RepoRoot:  root,
		EnvFile:   filepath.Join(root, ".env"),
		StacksDir: filepath.Join(root, "stacks"),
		Global:    testGlobalConfig(),
	}

	manager, err := NewManager(cfg)
	require.NoError(t, err)

	env, err := manager.buildStackEnv("demo")
	require.NoError(t, err)

	require.Equal(t, filepath.Join(root, ".ssd_pool", "demo"), env["STACKR_PROV_POOL_SSD"])
	require.Equal(t, filepath.Join(root, ".hdd_pool", "demo"), env["STACKR_PROV_POOL_HDD"])
	require.Equal(t, "demo.localhost", env["STACKR_PROV_DOMAIN"])
	require.Equal(t, "test_value", env["TEST_VAR"]) // Global env var from config
	require.Equal(t, "demo-value", env["STACK_SPECIFIC"])
}

func TestPoolValidation(t *testing.T) {
	t.Run("configured pool creates directory", func(t *testing.T) {
		root := t.TempDir()
		makeDirs(t, root, "stacks/demo")
		writeFile(t, filepath.Join(root, ".env"), "")
		writeFile(t, filepath.Join(root, "stacks/demo/docker-compose.yml"), `
services:
  app:
    image: nginx
    volumes:
      - ${STACKR_PROV_POOL_SSD}:/data
`)

		cfg := config.Config{
			RepoRoot:  root,
			EnvFile:   filepath.Join(root, ".env"),
			StacksDir: filepath.Join(root, "stacks"),
			Global:    testGlobalConfig(),
		}

		stubDocker(t)

		manager, err := NewManager(cfg)
		require.NoError(t, err)

		opts := Options{Stacks: []string{"demo"}, Update: true}
		require.NoError(t, manager.Run(context.Background(), opts))

		// Verify pool directory was created
		poolPath := filepath.Join(root, ".ssd_pool", "demo")
		require.DirExists(t, poolPath)
	})

	t.Run("unconfigured pool returns error", func(t *testing.T) {
		root := t.TempDir()
		makeDirs(t, root, "stacks/demo")
		writeFile(t, filepath.Join(root, ".env"), "")
		writeFile(t, filepath.Join(root, "stacks/demo/docker-compose.yml"), `
services:
  app:
    image: nginx
    volumes:
      - ${STACKR_PROV_POOL_NVME}:/data
`)

		cfg := config.Config{
			RepoRoot:  root,
			EnvFile:   filepath.Join(root, ".env"),
			StacksDir: filepath.Join(root, "stacks"),
			Global:    testGlobalConfig(),
		}

		stubDocker(t)

		manager, err := NewManager(cfg)
		require.NoError(t, err)

		opts := Options{Stacks: []string{"demo"}, Update: true}
		err = manager.Run(context.Background(), opts)
		require.Error(t, err)
		require.Contains(t, err.Error(), "STACKR_PROV_POOL_NVME")
		require.Contains(t, err.Error(), "not configured in paths.pools")
	})
}

func testGlobalConfig() config.GlobalConfig {
	return config.GlobalConfig{
		HTTP: config.HTTPConfig{BaseDomain: "localhost"},
		Paths: config.PathsConfig{
			BackupDir: "./backups",
			Pools: map[string]string{
				"SSD": ".ssd_pool",
				"HDD": ".hdd_pool",
			},
			Custom: map[string]string{},
		},
		Env: config.EnvConfig{
			Global: map[string]string{
				"TEST_VAR": "test_value",
			},
			Stacks: map[string]map[string]string{
				"demo": {
					"STACK_SPECIFIC": "demo-value",
				},
			},
		},
	}
}
