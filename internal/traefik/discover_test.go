package traefik

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// stubProbeable returns a test server that responds 200 to /api/overview.
// The discoverer probes that endpoint — anything else is a 404.
func stubProbeable(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/overview" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestResolve_EnvOverrideWinsWithoutProbing(t *testing.T) {
	// The env override must NOT probe — operators set it explicitly when
	// they want the daemon to call a specific URL even if it's currently
	// unreachable (warm-up window during a deploy, e.g.).
	t.Setenv(EnvAPIURLOverride, "http://example.com:9999")

	d := NewDiscoverer("")
	require.Equal(t, "http://example.com:9999", d.Resolve(context.Background()))
}

func TestResolve_ConfigUsedWhenNoEnv(t *testing.T) {
	t.Setenv(EnvAPIURLOverride, "")

	d := NewDiscoverer("http://my-traefik:8080")
	require.Equal(t, "http://my-traefik:8080", d.Resolve(context.Background()))
}

func TestResolve_FallsBackToFirstReachableCandidate(t *testing.T) {
	// Override CandidateURLs for the test so the probe hits our stub
	// instead of a real traefik. The first candidate is bogus on
	// purpose — verifies the discoverer skips dead URLs and tries the
	// next one.
	t.Setenv(EnvAPIURLOverride, "")
	srv := stubProbeable(t)

	originalCandidates := CandidateURLs
	CandidateURLs = []string{"http://127.0.0.1:1", srv.URL}
	t.Cleanup(func() { CandidateURLs = originalCandidates })

	d := NewDiscoverer("")
	got := d.Resolve(context.Background())
	require.Equal(t, srv.URL, got,
		"first reachable candidate must win — silent skipping of bogus entries is what makes autodiscovery painless")
}

func TestResolve_ReturnsEmptyWhenNoCandidatesRespond(t *testing.T) {
	t.Setenv(EnvAPIURLOverride, "")
	originalCandidates := CandidateURLs
	CandidateURLs = []string{"http://127.0.0.1:1", "http://127.0.0.1:2"}
	t.Cleanup(func() { CandidateURLs = originalCandidates })

	d := NewDiscoverer("")
	require.Equal(t, "", d.Resolve(context.Background()),
		"no probes responding must surface as empty string so the handler can render the 'not detected' state")
}

func TestResolve_CachesSuccessfulProbe(t *testing.T) {
	// Probing isn't free — once we have a working URL, subsequent calls
	// within cacheTTL should reuse it. The test asserts the cached value
	// is returned after the underlying server is closed; without caching
	// the second Resolve would re-probe and fail.
	t.Setenv(EnvAPIURLOverride, "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	originalCandidates := CandidateURLs
	CandidateURLs = []string{srv.URL}
	t.Cleanup(func() { CandidateURLs = originalCandidates })

	d := NewDiscoverer("")
	first := d.Resolve(context.Background())
	require.Equal(t, srv.URL, first)

	srv.Close()
	second := d.Resolve(context.Background())
	require.Equal(t, srv.URL, second,
		"a successful resolve must be cached — re-probing on every page load would burn 2s on the slow path")
}
