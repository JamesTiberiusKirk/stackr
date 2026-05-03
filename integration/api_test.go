//go:build integration

// Package integration holds end-to-end tests that exercise the daemon
// across multiple internal packages (HTTP → service → runner → docker → db).
// They live outside internal/ so the public API is the only surface tested,
// matching how external clients (CLI, CI webhooks) reach the daemon.
package integration

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

	"github.com/jamestiberiuskirk/stackr/internal/api"
	"github.com/jamestiberiuskirk/stackr/internal/db"
	"github.com/jamestiberiuskirk/stackr/internal/repo"
	"github.com/jamestiberiuskirk/stackr/internal/repo/sqlite"
	"github.com/jamestiberiuskirk/stackr/internal/service"
	"github.com/jamestiberiuskirk/stackr/internal/testutil"
)

const testToken = "test-secret-token"

// apiTestEnv bundles everything a test needs to drive the daemon and verify
// side effects. Returning the store explicitly is the whole point of this
// type — it lets each test go back and check that DB rows actually landed,
// catching the class of bug where the API responds "ok" but the deployment
// row was never persisted.
type apiTestEnv struct {
	server    *httptest.Server
	root      string
	stackName string
	store     repo.Store
}

func setupAPITest(t *testing.T, opts ...testutil.RepoOption) *apiTestEnv {
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

	return &apiTestEnv{
		server:    httpServer,
		root:      root,
		stackName: stackName,
		store:     store,
	}
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
	env := setupAPITest(t)

	resp := doRequest(t, env.server, http.MethodGet, "/api/health", nil, "")
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	require.Equal(t, "healthy", result["status"])
}

func TestAPIDeploy(t *testing.T) {
	t.Run("MissingAuthReturns401", func(t *testing.T) {
		env := setupAPITest(t)

		body := map[string]string{"stack": env.stackName, "tag": "v1.0.0"}
		resp := doRequest(t, env.server, http.MethodPost, "/api/deploy", body, "")
		defer resp.Body.Close()

		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("WrongTokenReturns401", func(t *testing.T) {
		env := setupAPITest(t)

		body := map[string]string{"stack": env.stackName, "tag": "v1.0.0"}
		resp := doRequest(t, env.server, http.MethodPost, "/api/deploy", body, "wrong-token")
		defer resp.Body.Close()

		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("InvalidBodyReturns400", func(t *testing.T) {
		env := setupAPITest(t)

		req, err := http.NewRequest(http.MethodPost, env.server.URL+"/api/deploy",
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
		env := setupAPITest(t)

		body := map[string]string{"tag": "v1.0.0"}
		resp := doRequest(t, env.server, http.MethodPost, "/api/deploy", body, testToken)
		defer resp.Body.Close()

		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	// Contract drift vs legacy daemon: unknown stack now yields 404 (was 400).
	// The new daemon distinguishes "not found" from "invalid input"; CLI clients
	// should accept either status as a non-success outcome.
	t.Run("NonExistentStackReturns404", func(t *testing.T) {
		env := setupAPITest(t)

		body := map[string]string{"stack": "doesnotexist", "tag": "v1.0.0"}
		resp := doRequest(t, env.server, http.MethodPost, "/api/deploy", body, testToken)
		defer resp.Body.Close()

		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	// Authenticated path-traversal returns 400 — the request is malformed,
	// not "not found". This is also defense-in-depth: ValidateStackName
	// rejects the name before it reaches the filesystem, so a probe like
	// {"stack":"../../etc"} can't enumerate parent directories.
	t.Run("PathTraversalStackReturns400", func(t *testing.T) {
		env := setupAPITest(t)

		body := map[string]string{"stack": "../../etc", "tag": "v1.0.0"}
		resp := doRequest(t, env.server, http.MethodPost, "/api/deploy", body, testToken)
		defer resp.Body.Close()

		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("AutoDeployDisabledReturns403", func(t *testing.T) {
		env := setupAPITest(t,
			testutil.WithComposeContent(`services:
  web:
    image: nginx:alpine
    labels:
      stackr.deploy.auto: "false"
    ports:
      - "0:80"
`))

		body := map[string]string{"stack": env.stackName, "tag": "v1.0.0"}
		resp := doRequest(t, env.server, http.MethodPost, "/api/deploy", body, testToken)
		defer resp.Body.Close()

		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	// SuccessfulDeployReturns200 verifies BOTH the response shape AND that a
	// matching deployment row was persisted with the final outcome. Reading
	// the row back via the store catches the class of bug where the response
	// is built correctly but the persistence call was dropped or fails
	// silently — a 200 alone wouldn't notice.
	t.Run("SuccessfulDeployReturns200", func(t *testing.T) {
		env := setupAPITest(t)

		body := map[string]string{"stack": env.stackName, "tag": "latest"}
		resp := doRequest(t, env.server, http.MethodPost, "/api/deploy", body, testToken)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		var result map[string]interface{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
		require.Equal(t, "success", result["status"])
		require.Equal(t, env.stackName, result["stack"])
		require.Equal(t, "latest", result["tag"])

		id, _ := result["id"].(string)
		require.NotEmpty(t, id, "response must include the deployment id")

		// The row must exist in the store with the same final state the
		// response advertised.
		row, err := env.store.GetDeploymentByID(context.Background(), id)
		require.NoError(t, err)
		require.NotNil(t, row, "deployment row %q should be persisted", id)
		require.Equal(t, env.stackName, row.Stack)
		require.Equal(t, "latest", row.Tag)
		require.Equal(t, repo.DeploymentStatusSuccess, row.Status)
		require.Equal(t, repo.DeploymentTriggerAPI, row.Trigger)
		require.NotNil(t, row.FinishedAt, "successful deployments must have a FinishedAt timestamp")
		require.Empty(t, row.Error)

		// And it should be discoverable via ListDeployments — the dashboard /
		// CLI listing path must see it too, not just the by-id lookup.
		list, err := env.store.ListDeployments(context.Background(), repo.DeploymentFilter{Stack: env.stackName})
		require.NoError(t, err)
		require.Len(t, list, 1)
		require.Equal(t, id, list[0].ID)
	})

	t.Run("InvalidTagFormatReturns400", func(t *testing.T) {
		env := setupAPITest(t)

		body := map[string]string{"stack": env.stackName, "tag": "not a valid tag!!!"}
		resp := doRequest(t, env.server, http.MethodPost, "/api/deploy", body, testToken)
		defer resp.Body.Close()

		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

// TestAPIDeployCreatesStack verifies a successful deploy writes the resolved
// tag back into the repo .env file AND persists the deployment row. The two
// side-effects together prove the daemon really did the work: stack files
// changed on disk and a record landed in the store.
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

	var result map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	id, _ := result["id"].(string)
	require.NotEmpty(t, id)

	data, err := os.ReadFile(filepath.Join(root, ".env"))
	require.NoError(t, err)
	require.Contains(t, string(data), tagEnv+"=latest")

	row, err := store.GetDeploymentByID(context.Background(), id)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, repo.DeploymentStatusSuccess, row.Status)
}
