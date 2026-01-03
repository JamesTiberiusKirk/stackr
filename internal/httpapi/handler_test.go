package httpapi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jamestiberiuskirk/stackr/internal/config"
)

func TestIsAutoDeployEnabled(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	stacksDir := filepath.Join(tmpDir, "stacks")
	require.NoError(t, os.MkdirAll(stacksDir, 0o755))

	tests := []struct {
		name        string
		stackName   string
		compose     string
		envContent  string
		wantEnabled bool
		wantErr     bool
	}{
		{
			name:      "no label defaults to enabled",
			stackName: "test1",
			compose: `
services:
  app:
    image: myapp:latest
`,
			wantEnabled: true,
			wantErr:     false,
		},
		{
			name:      "explicit true enables deployment",
			stackName: "test2",
			compose: `
services:
  app:
    image: myapp:latest
    labels:
      - stackr.deploy.auto=true
`,
			wantEnabled: true,
			wantErr:     false,
		},
		{
			name:      "explicit false disables deployment",
			stackName: "test3",
			compose: `
services:
  app:
    image: myapp:latest
    labels:
      - stackr.deploy.auto=false
`,
			wantEnabled: false,
			wantErr:     false,
		},
		{
			name:      "env var reference resolves to true",
			stackName: "test4",
			compose: `
services:
  app:
    image: myapp:latest
    labels:
      stackr.deploy.auto: ${MYAPP_AUTODEPLOY}
`,
			envContent:  "MYAPP_AUTODEPLOY=true\n",
			wantEnabled: true,
			wantErr:     false,
		},
		{
			name:      "env var reference resolves to false",
			stackName: "test5",
			compose: `
services:
  app:
    image: myapp:latest
    labels:
      stackr.deploy.auto: ${MYAPP_AUTODEPLOY}
`,
			envContent:  "MYAPP_AUTODEPLOY=false\n",
			wantEnabled: false,
			wantErr:     false,
		},
		{
			name:      "invalid value disables deployment",
			stackName: "test6",
			compose: `
services:
  app:
    image: myapp:latest
    labels:
      - stackr.deploy.auto=notabool
`,
			wantEnabled: false,
			wantErr:     false,
		},
		{
			name:      "any service disabled blocks deployment",
			stackName: "test7",
			compose: `
services:
  app1:
    image: myapp1:latest
    labels:
      - stackr.deploy.auto=true
  app2:
    image: myapp2:latest
    labels:
      - stackr.deploy.auto=false
`,
			wantEnabled: false,
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create stack directory
			stackDir := filepath.Join(stacksDir, tt.stackName)
			require.NoError(t, os.MkdirAll(stackDir, 0o755))

			// Write docker-compose.yml
			composePath := filepath.Join(stackDir, "docker-compose.yml")
			require.NoError(t, os.WriteFile(composePath, []byte(tt.compose), 0o644))

			// Write .env file if provided
			envPath := filepath.Join(tmpDir, ".env")
			if tt.envContent != "" {
				require.NoError(t, os.WriteFile(envPath, []byte(tt.envContent), 0o644))
			}

			cfg := config.Config{
				StacksDir: stacksDir,
				RepoRoot:  tmpDir,
				EnvFile:   envPath,
			}

			h := &Handler{cfg: cfg}
			enabled, err := h.isAutoDeployEnabled(tt.stackName)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.wantEnabled, enabled)
			}
		})
	}
}

func TestLoadEnvFile(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()

	tests := []struct {
		name        string
		envContent  string
		wantVars    map[string]string
		wantErr     bool
		createFile  bool
	}{
		{
			name:       "missing file returns empty map",
			createFile: false,
			wantVars:   map[string]string{},
			wantErr:    false,
		},
		{
			name:       "parses simple key-value pairs",
			createFile: true,
			envContent: "FOO=bar\nBAZ=qux\n",
			wantVars:   map[string]string{"FOO": "bar", "BAZ": "qux"},
			wantErr:    false,
		},
		{
			name:       "ignores comments and empty lines",
			createFile: true,
			envContent: "# Comment\nFOO=bar\n\nBAZ=qux\n",
			wantVars:   map[string]string{"FOO": "bar", "BAZ": "qux"},
			wantErr:    false,
		},
		{
			name:       "handles quoted values",
			createFile: true,
			envContent: "FOO=\"bar baz\"\nQUX='hello world'\n",
			wantVars:   map[string]string{"FOO": "bar baz", "QUX": "hello world"},
			wantErr:    false,
		},
		{
			name:       "handles values with equals signs",
			createFile: true,
			envContent: "CONNECTION_STRING=postgres://user:pass@host/db?param=value\n",
			wantVars:   map[string]string{"CONNECTION_STRING": "postgres://user:pass@host/db?param=value"},
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envPath := filepath.Join(tmpDir, ".env."+tt.name)

			if tt.createFile {
				require.NoError(t, os.WriteFile(envPath, []byte(tt.envContent), 0o644))
			}

			cfg := config.Config{
				RepoRoot: tmpDir,
				EnvFile:  envPath,
			}

			h := &Handler{cfg: cfg}
			vars, err := h.loadEnvFile()

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.wantVars, vars)
			}
		})
	}
}

func TestResolveEnvVars(t *testing.T) {
	t.Helper()
	h := &Handler{}
	envVars := map[string]string{
		"FOO": "bar",
		"BAZ": "qux",
		"MYAPP_AUTODEPLOY": "true",
	}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no variables",
			input: "hello world",
			want:  "hello world",
		},
		{
			name:  "single variable",
			input: "${FOO}",
			want:  "bar",
		},
		{
			name:  "multiple variables",
			input: "${FOO} and ${BAZ}",
			want:  "bar and qux",
		},
		{
			name:  "undefined variable unchanged",
			input: "${UNDEFINED}",
			want:  "${UNDEFINED}",
		},
		{
			name:  "mixed defined and undefined",
			input: "${FOO} and ${UNDEFINED}",
			want:  "bar and ${UNDEFINED}",
		},
		{
			name:  "autodeploy variable",
			input: "${MYAPP_AUTODEPLOY}",
			want:  "true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := h.resolveEnvVars(tt.input, envVars)
			require.Equal(t, tt.want, result)
		})
	}
}
