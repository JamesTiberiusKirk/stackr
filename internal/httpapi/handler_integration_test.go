//go:build integration

package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jamestiberiuskirk/stackr/internal/runner"
	"github.com/jamestiberiuskirk/stackr/internal/testutil"
)

const testToken = "test-secret-token"

func setupHTTPTest(t *testing.T, opts ...testutil.RepoOption) (*httptest.Server, string, string) {
	t.Helper()
	testutil.RequireDockerAvailable(t)

	defaults := []testutil.RepoOption{
		testutil.WithComposeContent(`services:
  web:
    image: nginx:alpine
    ports:
      - "0:80"
`),
	}
	opts = append(defaults, opts...)
	root, stackName := testutil.SetupTestRepo(t, opts...)
	testutil.CleanupComposeProjectByDir(t, root, stackName)

	// Write the tag env var matching the stack name
	tagEnv := testutil.TagEnvVar(stackName)
	envPath := filepath.Join(root, ".env")
	require.NoError(t, os.WriteFile(envPath, []byte(tagEnv+"=alpine\n"), 0o644))

	cfg := testutil.BuildConfigDirect(root)
	cfg.Token = testToken

	r := runner.New(cfg)
	handler := New(cfg, r)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return server, root, stackName
}

func doRequest(t *testing.T, server *httptest.Server, method, path string, body interface{}, token string) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, server.URL+path, reader)
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func TestHealthEndpoint(t *testing.T) {
	server, _, _ := setupHTTPTest(t)

	resp := doRequest(t, server, http.MethodGet, "/healthz", nil, "")
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	require.Equal(t, "ok", result["status"])
}

func TestDeployEndpoint(t *testing.T) {
	t.Run("MissingAuthReturns401", func(t *testing.T) {
		server, _, stackName := setupHTTPTest(t)

		body := map[string]string{"stack": stackName, "tag": "v1.0.0"}
		resp := doRequest(t, server, http.MethodPost, "/deploy", body, "")
		defer resp.Body.Close()

		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("WrongTokenReturns401", func(t *testing.T) {
		server, _, stackName := setupHTTPTest(t)

		body := map[string]string{"stack": stackName, "tag": "v1.0.0"}
		resp := doRequest(t, server, http.MethodPost, "/deploy", body, "wrong-token")
		defer resp.Body.Close()

		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("InvalidBodyReturns400", func(t *testing.T) {
		server, _, _ := setupHTTPTest(t)

		req, err := http.NewRequest(http.MethodPost, server.URL+"/deploy",
			bytes.NewReader([]byte("not json")))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testToken)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("MissingStackReturns400", func(t *testing.T) {
		server, _, _ := setupHTTPTest(t)

		body := map[string]string{"tag": "v1.0.0"}
		resp := doRequest(t, server, http.MethodPost, "/deploy", body, testToken)
		defer resp.Body.Close()

		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("NonExistentStackReturns400", func(t *testing.T) {
		server, _, _ := setupHTTPTest(t)

		body := map[string]string{"stack": "doesnotexist", "tag": "v1.0.0"}
		resp := doRequest(t, server, http.MethodPost, "/deploy", body, testToken)
		defer resp.Body.Close()

		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("AutoDeployDisabledReturns403", func(t *testing.T) {
		server, _, stackName := setupHTTPTest(t,
			testutil.WithComposeContent(`services:
  web:
    image: nginx:alpine
    labels:
      stackr.deploy.auto: "false"
    ports:
      - "0:80"
`))

		body := map[string]string{"stack": stackName, "tag": "v1.0.0"}
		resp := doRequest(t, server, http.MethodPost, "/deploy", body, testToken)
		defer resp.Body.Close()

		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("SuccessfulDeployReturns200", func(t *testing.T) {
		server, _, stackName := setupHTTPTest(t)

		body := map[string]string{"stack": stackName, "tag": "latest"}
		resp := doRequest(t, server, http.MethodPost, "/deploy", body, testToken)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		var result map[string]interface{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
		require.Equal(t, "ok", result["status"])
	})

	t.Run("InvalidTagFormatReturns400", func(t *testing.T) {
		server, _, stackName := setupHTTPTest(t)

		body := map[string]string{"stack": stackName, "tag": "not a valid tag!!!"}
		resp := doRequest(t, server, http.MethodPost, "/deploy", body, testToken)
		defer resp.Body.Close()

		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

func TestDeployEndpointCreatesStack(t *testing.T) {
	testutil.RequireDockerAvailable(t)

	stackName := testutil.UniqueStackName()
	tagEnv := testutil.TagEnvVar(stackName)

	root, _ := testutil.SetupTestRepo(t,
		testutil.WithStackName(stackName),
		testutil.WithComposeContent(fmt.Sprintf(`services:
  web:
    image: nginx:${%s}
    ports:
      - "0:80"
`, tagEnv)),
		testutil.WithEnvContent(tagEnv+"=alpine\n"))
	testutil.CleanupComposeProjectByDir(t, root, stackName)

	cfg := testutil.BuildConfigDirect(root)
	cfg.Token = testToken

	r := runner.New(cfg)
	handler := New(cfg, r)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	body := map[string]string{"stack": stackName, "tag": "latest"}
	resp := doRequest(t, server, http.MethodPost, "/deploy", body, testToken)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify the tag was written to the env file
	data, err := os.ReadFile(filepath.Join(root, ".env"))
	require.NoError(t, err)
	require.Contains(t, string(data), tagEnv+"=latest")
}
