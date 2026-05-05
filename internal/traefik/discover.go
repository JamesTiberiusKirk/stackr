package traefik

import (
	"context"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// EnvAPIURLOverride is the env var operators set to pin the traefik API
// URL — wins over config and autodiscovery. Useful when traefik runs at a
// non-default port or on a separate host.
const EnvAPIURLOverride = "STACKR_TRAEFIK_API_URL"

// CandidateURLs are the well-known fallbacks the discoverer probes when
// the operator hasn't pinned a URL.
//
//   - http://traefik:8080 — daemon on the traefik docker network. The
//     prod expectation: stackr ships with traefik on a shared docker
//     network and uses container DNS to find it.
//
//   - http://localhost:8081 — local dev. The sandbox traefik publishes
//     its dashboard/API on host port 8081 (mapped from internal 8080).
var CandidateURLs = []string{
	"http://traefik:8080",
	"http://localhost:8081",
}

// probeTimeout caps a single autodiscovery probe. The full discovery
// loop blocks the page request, so we keep this aggressive — a missing
// traefik shouldn't make the routes page slow.
const probeTimeout = 1 * time.Second

// cacheTTL is how long a successfully-resolved URL is reused before we
// re-probe. Short enough that a traefik restart on a different network
// surfaces fast; long enough that a busy /routes page doesn't probe per
// request.
const cacheTTL = 30 * time.Second

// Discoverer resolves the traefik API URL using env > config > probes,
// caching the answer briefly. Use NewDiscoverer once at daemon boot and
// call Resolve from request handlers.
type Discoverer struct {
	configURL string

	mu       sync.Mutex
	cached   string
	cachedAt time.Time
}

// NewDiscoverer wires a discoverer with the API URL from stackr.yaml
// (empty string = none configured). The env-var override and autodiscovery
// candidates are baked in.
func NewDiscoverer(configURL string) *Discoverer {
	return &Discoverer{configURL: strings.TrimSpace(configURL)}
}

// Resolve returns the first reachable traefik API URL, or "" if none
// respond. Lookup order:
//
//  1. STACKR_TRAEFIK_API_URL env var (returned without probing — the
//     operator opted in, we don't second-guess connectivity)
//  2. traefik.api_url from stackr.yaml (also returned without probing)
//  3. CandidateURLs probed in order; first 200 from /api/overview wins
//
// The resolved URL is cached for cacheTTL. A failed resolution caches
// "" for the same duration, so a missing traefik doesn't hammer the
// candidates on every page load.
func (d *Discoverer) Resolve(ctx context.Context) string {
	if v := strings.TrimSpace(os.Getenv(EnvAPIURLOverride)); v != "" {
		return v
	}
	if d.configURL != "" {
		return d.configURL
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if time.Since(d.cachedAt) < cacheTTL {
		return d.cached
	}

	for _, candidate := range CandidateURLs {
		if probe(ctx, candidate) {
			d.cached = candidate
			d.cachedAt = time.Now()
			return candidate
		}
	}
	d.cached = ""
	d.cachedAt = time.Now()
	return ""
}

// probe issues a HEAD-equivalent GET against /api/overview and returns
// true on 2xx. Uses a per-call client with probeTimeout so a hung
// candidate can't stall the discoverer.
func probe(ctx context.Context, baseURL string) bool {
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	url := strings.TrimRight(baseURL, "/") + "/api/overview"
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: probeTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}
