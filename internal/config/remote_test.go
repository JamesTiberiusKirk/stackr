package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadRemoteStackDefinition(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	stacksDir := filepath.Join(tmpDir, "stacks")

	tests := []struct {
		name      string
		stackName string
		content   string
		wantErr   bool
		validate  func(t *testing.T, def *RemoteStackDefinition)
	}{
		{
			name:      "valid definition with all fields",
			stackName: "myapp",
			content: `
remote_repo:
  url: git@github.com:org/myapp.git
  branch: main
  path: deploy
  release:
    type: tag
    ref: ${MYAPP_VERSION}
`,
			wantErr: false,
			validate: func(t *testing.T, def *RemoteStackDefinition) {
				require.Equal(t, "git@github.com:org/myapp.git", def.RemoteRepo.URL)
				require.Equal(t, "main", def.RemoteRepo.Branch)
				require.Equal(t, "deploy", def.RemoteRepo.Path)
				require.Equal(t, "tag", def.RemoteRepo.Release.Type)
				require.Equal(t, "${MYAPP_VERSION}", def.RemoteRepo.Release.Ref)
			},
		},
		{
			name:      "valid definition with defaults",
			stackName: "simple",
			content: `
remote_repo:
  url: git@github.com:org/simple.git
  release:
    type: commit
    ref: abc123
`,
			wantErr: false,
			validate: func(t *testing.T, def *RemoteStackDefinition) {
				require.Equal(t, "git@github.com:org/simple.git", def.RemoteRepo.URL)
				require.Equal(t, "main", def.RemoteRepo.Branch) // Default
				require.Equal(t, ".", def.RemoteRepo.Path)      // Default
				require.Equal(t, "commit", def.RemoteRepo.Release.Type)
				require.Equal(t, "abc123", def.RemoteRepo.Release.Ref)
			},
		},
		{
			name:      "missing url",
			stackName: "bad1",
			content: `
remote_repo:
  branch: main
  release:
    type: tag
    ref: v1.0.0
`,
			wantErr: true,
		},
		{
			name:      "missing release type",
			stackName: "bad2",
			content: `
remote_repo:
  url: git@github.com:org/app.git
  release:
    ref: v1.0.0
`,
			wantErr: true,
		},
		{
			name:      "invalid release type",
			stackName: "bad3",
			content: `
remote_repo:
  url: git@github.com:org/app.git
  release:
    type: branch
    ref: v1.0.0
`,
			wantErr: true,
		},
		{
			name:      "missing release ref",
			stackName: "bad4",
			content: `
remote_repo:
  url: git@github.com:org/app.git
  release:
    type: tag
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create stack directory
			stackDir := filepath.Join(stacksDir, tt.stackName)
			require.NoError(t, os.MkdirAll(stackDir, 0o755))

			// Write stackr.yaml
			stackrFile := filepath.Join(stackDir, "stackr-repo.yml")
			require.NoError(t, os.WriteFile(stackrFile, []byte(tt.content), 0o644))

			// Load definition
			def, err := LoadRemoteStackDefinition(stacksDir, tt.stackName)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, def)
				}
			}
		})
	}
}

func TestLoadDeploymentConfig(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()

	tests := []struct {
		name     string
		content  string
		exists   bool
		wantErr  bool
		validate func(t *testing.T, cfg *DeploymentConfig)
	}{
		{
			name: "with env vars",
			content: `
env:
  CUSTOM_VAR: value
  DOMAIN: myapp.example.com
`,
			exists:  true,
			wantErr: false,
			validate: func(t *testing.T, cfg *DeploymentConfig) {
				require.Equal(t, "value", cfg.Env["CUSTOM_VAR"])
				require.Equal(t, "myapp.example.com", cfg.Env["DOMAIN"])
			},
		},
		{
			name:    "empty config",
			content: ``,
			exists:  true,
			wantErr: false,
			validate: func(t *testing.T, cfg *DeploymentConfig) {
				require.NotNil(t, cfg.Env)
				require.Len(t, cfg.Env, 0)
			},
		},
		{
			name:    "missing file",
			exists:  false,
			wantErr: false, // Should not error, file is optional
			validate: func(t *testing.T, cfg *DeploymentConfig) {
				require.NotNil(t, cfg.Env)
				require.Len(t, cfg.Env, 0)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoDir := filepath.Join(tmpDir, tt.name)
			require.NoError(t, os.MkdirAll(repoDir, 0o755))

			if tt.exists {
				deployFile := filepath.Join(repoDir, ".stackr-deployment.yaml")
				require.NoError(t, os.WriteFile(deployFile, []byte(tt.content), 0o644))
			}

			cfg, err := LoadDeploymentConfig(repoDir)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, cfg)
				}
			}
		})
	}
}

func TestResolveVersionRef(t *testing.T) {
	t.Helper()

	tests := []struct {
		name    string
		ref     string
		envVars map[string]string
		want    string
		wantErr bool
	}{
		{
			name:    "no variables",
			ref:     "v1.2.3",
			envVars: map[string]string{},
			want:    "v1.2.3",
			wantErr: false,
		},
		{
			name: "single variable",
			ref:  "${MYAPP_VERSION}",
			envVars: map[string]string{
				"MYAPP_VERSION": "v1.2.3",
			},
			want:    "v1.2.3",
			wantErr: false,
		},
		{
			name: "variable with prefix",
			ref:  "release-${VERSION}",
			envVars: map[string]string{
				"VERSION": "1.0.0",
			},
			want:    "release-1.0.0",
			wantErr: false,
		},
		{
			name: "multiple variables",
			ref:  "${PREFIX}_${VERSION}",
			envVars: map[string]string{
				"PREFIX":  "v",
				"VERSION": "2.0.0",
			},
			want:    "v_2.0.0",
			wantErr: false,
		},
		{
			name:    "undefined variable",
			ref:     "${UNDEFINED_VAR}",
			envVars: map[string]string{},
			wantErr: true,
		},
		{
			name:    "empty ref",
			ref:     "",
			envVars: map[string]string{},
			wantErr: true,
		},
		{
			name: "whitespace trimmed",
			ref:  "  ${VERSION}  ",
			envVars: map[string]string{
				"VERSION": "v1.0.0",
			},
			want:    "v1.0.0",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveVersionRef(tt.ref, tt.envVars)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, got)
			}
		})
	}
}
