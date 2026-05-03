package stackcmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jamestiberiuskirk/stackr/internal/config"
)

func TestIsAutoDeployEnabled(t *testing.T) {
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
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stackDir := filepath.Join(stacksDir, tt.stackName)
			require.NoError(t, os.MkdirAll(stackDir, 0o755))

			composePath := filepath.Join(stackDir, "docker-compose.yml")
			require.NoError(t, os.WriteFile(composePath, []byte(tt.compose), 0o644))

			envPath := filepath.Join(tmpDir, ".env")
			if tt.envContent != "" {
				require.NoError(t, os.WriteFile(envPath, []byte(tt.envContent), 0o644))
			}

			cfg := config.Config{
				StacksDir: stacksDir,
				RepoRoot:  tmpDir,
				EnvFile:   envPath,
			}

			enabled, err := IsAutoDeployEnabled(cfg, tt.stackName)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.wantEnabled, enabled)
			}
		})
	}
}
