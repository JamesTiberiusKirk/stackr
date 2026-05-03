//go:build integration

// Integration tests for the daemon's HTTP API. These exercise the full route
// stack — middleware, bearer auth, service layer, real runner — against a
// real SQLite store and (for deploy paths) a real Docker daemon.
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/FyrmForge/hamr/pkg/server"
	"github.com/stretchr/testify/require"

	"github.com/jamestiberiuskirk/stackr/internal/testutil"

	"github.com/jamestiberiuskirk/stackr/internal/api"
	"github.com/jamestiberiuskirk/stackr/internal/db"
	"github.com/jamestiberiuskirk/stackr/internal/repo/sqlite"
	"github.com/jamestiberiuskirk/stackr/internal/service"
)

const testToken = "test-secret-token"

// setupAPITest spins up a fresh in-memory SQLite store, a StackrService backed
// by a temp test repo, and an httptest.Server fronting the registered API
// routes. Returns the server, repo root, and chosen stack name.
func setupAPITest(t *testing.T, opts ...testutil.RepoOption) (*httptest.Server, string, string) {
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

	tagEnv := testutil.TagEnvVar(stackName)
	envPath := filepath.Join(root, ".env")
	require.NoError(t, os.WriteFile(envPath, []byte(tagEnv+"=alpine\n"), 0o644))

	cfg := testutil.BuildConfigDirect(root)
	cfg.Token = testToken

	database, err := db.ConnectContext(context.Background(), ":memory:")
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(database))
	store := sqlite.NewStore(database)

	stackrService := service.NewStackrService(cfg, store)

	srv, err := server.New(server.WithDevMode(true))
	require.NoError(t, err)

	api.RegisterRoutes(srv, &api.Deps{
		Store:  store,
		Stackr: stackrService,
	})

	httpServer := httptest.NewServer(srv.Echo())
	t.Cleanup(httpServer.Close)

	return httpServer, root, stackName
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

func TestAPIHealth(t *testing.T) {
	srv, _, _ := setupAPITest(t)

	resp := doRequest(t, srv, http.MethodGet, "/api/health", nil, "")
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	require.Equal(t, "healthy", result["status"])
}

func TestAPIDeploy(t *testing.T) {
	t.Run("MissingAuthReturns401", func(t *testing.T) {
		srv, _, stackName := setupAPITest(t)

		body := map[string]string{"stack": stackName, "tag": "v1.0.0"}
		resp := doRequest(t, srv, http.MethodPost, "/api/deploy", body, "")
		defer resp.Body.Close()

		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("WrongTokenReturns401", func(t *testing.T) {
		srv, _, stackName := setupAPITest(t)

		body := map[string]string{"stack": stackName, "tag": "v1.0.0"}
		resp := doRequest(t, srv, http.MethodPost, "/api/deploy", body, "wrong-token")
		defer resp.Body.Close()

		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("InvalidBodyReturns400", func(t *testing.T) {
		srv, _, _ := setupAPITest(t)

		req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/deploy",
			bytes.NewReader([]byte("not json")))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testToken)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("MissingStackReturns400", func(t *testing.T) {
		srv, _, _ := setupAPITest(t)

		body := map[string]string{"tag": "v1.0.0"}
		resp := doRequest(t, srv, http.MethodPost, "/api/deploy", body, testToken)
		defer resp.Body.Close()

		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	// Contract drift vs legacy daemon: unknown stack now yields 404 (was 400).
	// The new daemon distinguishes "not found" from "invalid input"; CLI clients
	// should accept either status as a non-success outcome.
	t.Run("NonExistentStackReturns404", func(t *testing.T) {
		srv, _, _ := setupAPITest(t)

		body := map[string]string{"stack": "doesnotexist", "tag": "v1.0.0"}
		resp := doRequest(t, srv, http.MethodPost, "/api/deploy", body, testToken)
		defer resp.Body.Close()

		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("AutoDeployDisabledReturns403", func(t *testing.T) {
		srv, _, stackName := setupAPITest(t,
			testutil.WithComposeContent(`services:
  web:
    image: nginx:alpine
    labels:
      stackr.deploy.auto: "false"
    ports:
      - "0:80"
`))

		body := map[string]string{"stack": stackName, "tag": "v1.0.0"}
		resp := doRequest(t, srv, http.MethodPost, "/api/deploy", body, testToken)
		defer resp.Body.Close()

		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("SuccessfulDeployReturns200", func(t *testing.T) {
		srv, _, stackName := setupAPITest(t)

		body := map[string]string{"stack": stackName, "tag": "latest"}
		resp := doRequest(t, srv, http.MethodPost, "/api/deploy", body, testToken)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		var result map[string]interface{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
		require.Equal(t, "success", result["status"])
		require.Equal(t, stackName, result["stack"])
		require.Equal(t, "latest", result["tag"])
		require.NotEmpty(t, result["id"])
	})

	t.Run("InvalidTagFormatReturns400", func(t *testing.T) {
		srv, _, stackName := setupAPITest(t)

		body := map[string]string{"stack": stackName, "tag": "not a valid tag!!!"}
		resp := doRequest(t, srv, http.MethodPost, "/api/deploy", body, testToken)
		defer resp.Body.Close()

		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

// TestAPIDeployCreatesStack verifies a successful deploy writes the resolved
// tag back into the repo .env file, matching the legacy daemon's behavior.
func TestAPIDeployCreatesStack(t *testing.T) {
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

	database, err := db.ConnectContext(context.Background(), ":memory:")
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(database))
	store := sqlite.NewStore(database)

	stackrService := service.NewStackrService(cfg, store)

	srv, err := server.New(server.WithDevMode(true))
	require.NoError(t, err)
	api.RegisterRoutes(srv, &api.Deps{Store: store, Stackr: stackrService})

	httpServer := httptest.NewServer(srv.Echo())
	t.Cleanup(httpServer.Close)

	body := map[string]string{"stack": stackName, "tag": "latest"}
	resp := doRequest(t, httpServer, http.MethodPost, "/api/deploy", body, testToken)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	data, err := os.ReadFile(filepath.Join(root, ".env"))
	require.NoError(t, err)
	require.Contains(t, string(data), tagEnv+"=latest")
}
