package traefik

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// stubTraefik returns an httptest server that mimics traefik's
// /api/http/routers endpoint — body is plumbed verbatim, status defaults
// to 200. Tests reach for it instead of touching a real traefik.
func stubTraefik(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/http/routers", r.URL.Path,
			"client must hit the canonical traefik routers path — anything else is a regression")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestListRouters_FiltersInternalProvider(t *testing.T) {
	// Traefik's own dashboard / api routers come back tagged
	// `provider=internal`. Showing them on /routes would clutter the page
	// with stuff users can't change — the filter is the contract.
	srv := stubTraefik(t, http.StatusOK, `[
		{"name":"api@internal","rule":"PathPrefix(`+"`"+`/api`+"`"+`)","service":"api@internal","entryPoints":["traefik"],"provider":"internal","status":"enabled"},
		{"name":"nginx@docker","rule":"Host(`+"`"+`nginx.localhost`+"`"+`)","service":"nginx@docker","entryPoints":["web"],"provider":"docker","status":"enabled"}
	]`)

	got, err := NewClient(srv.URL).ListRouters(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "nginx@docker", got[0].Name)
}

func TestListRouters_ExtractsHostsFromRule(t *testing.T) {
	srv := stubTraefik(t, http.StatusOK, `[
		{"name":"r@docker","rule":"Host(`+"`"+`a.local`+"`"+`) || Host(`+"`"+`b.local`+"`"+`)","service":"x@docker","entryPoints":["web"],"provider":"docker","status":"enabled"}
	]`)

	got, err := NewClient(srv.URL).ListRouters(context.Background())
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"a.local", "b.local"}, got[0].Hosts)
}

func TestListRouters_SortsByName(t *testing.T) {
	// Stable order matters: the /routes table renders in a list and the UI
	// would flicker on every refresh if we relied on the API's order.
	srv := stubTraefik(t, http.StatusOK, `[
		{"name":"zebra@docker","rule":"Host(`+"`"+`z.local`+"`"+`)","service":"z","entryPoints":["web"],"provider":"docker","status":"enabled"},
		{"name":"alpha@file","rule":"Host(`+"`"+`a.local`+"`"+`)","service":"a","entryPoints":["web"],"provider":"file","status":"enabled"}
	]`)

	got, err := NewClient(srv.URL).ListRouters(context.Background())
	require.NoError(t, err)
	require.Equal(t, "alpha@file", got[0].Name)
	require.Equal(t, "zebra@docker", got[1].Name)
}

func TestRouterURLs_PicksHTTPSFromSecureEntrypoint(t *testing.T) {
	r := Router{
		Hosts:       []string{"api.local"},
		EntryPoints: []string{"websecure"},
	}
	require.Equal(t, []string{"https://api.local"}, r.URLs())
}

func TestRouterURLs_DefaultsHTTP(t *testing.T) {
	r := Router{
		Hosts:       []string{"api.local"},
		EntryPoints: []string{"web"},
	}
	require.Equal(t, []string{"http://api.local"}, r.URLs())
}

func TestListRouters_PropagatesNon200(t *testing.T) {
	srv := stubTraefik(t, http.StatusInternalServerError, `nope`)
	_, err := NewClient(srv.URL).ListRouters(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "status 500")
}
