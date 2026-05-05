// Package traefik is a thin client for traefik's HTTP API. We hit it from
// the daemon to surface the routers traefik is actually serving — both
// docker-provider (compose-label) routes and file-provider routes — in one
// uniform list, regardless of where they were declared.
//
// The package only consumes the read-only API surface (routers, services,
// overview); it does not write traefik dynamic config.
package traefik

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// httpTimeout caps a single API call. Routes are listed on demand from the
// /routes page; the page should fail fast rather than hang the request.
const httpTimeout = 5 * time.Second

// Router is a single HTTP router as reported by traefik. Field set is the
// subset the UI needs; traefik returns more (priority, observability, etc.)
// that we deliberately ignore.
type Router struct {
	Name        string   `json:"name"`
	Rule        string   `json:"rule"`
	Service     string   `json:"service"`
	EntryPoints []string `json:"entryPoints"`
	Provider    string   `json:"provider"`
	Status      string   `json:"status"`

	// Hosts is derived from Rule by extracting Host(`…`) clauses. Routes
	// without an extractable host (HostRegexp, Path-only) come back with
	// an empty slice — handled by the UI.
	Hosts []string `json:"-"`
}

// URLs returns every host as a clickable URL using the entrypoint scheme
// convention (`*secure*` → https; otherwise http). Empty when Hosts is
// empty.
func (r Router) URLs() []string {
	if len(r.Hosts) == 0 {
		return nil
	}
	scheme := "http"
	for _, ep := range r.EntryPoints {
		if strings.Contains(strings.ToLower(ep), "secure") {
			scheme = "https"
			break
		}
	}
	out := make([]string, 0, len(r.Hosts))
	for _, host := range r.Hosts {
		out = append(out, scheme+"://"+host)
	}
	return out
}

// Client talks to a single traefik API base URL. Construct one per resolved
// URL — discovery (see discover.go) decides which URL to hand in.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient returns a client bound to baseURL. baseURL is the API root
// without trailing slash, e.g. "http://traefik:8080".
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: httpTimeout},
	}
}

// BaseURL returns the configured API root — useful for diagnostics
// ("served from <url>") on the routes page.
func (c *Client) BaseURL() string {
	return c.baseURL
}

// ListRouters returns every HTTP router known to traefik, except those
// owned by the `internal` provider (traefik's own dashboard / API
// routes). Sorted by name for stable rendering.
func (c *Client) ListRouters(ctx context.Context) ([]Router, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/http/routers", nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call traefik api: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("traefik api returned status %d", resp.StatusCode)
	}

	var raw []Router
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode routers: %w", err)
	}

	out := make([]Router, 0, len(raw))
	for _, r := range raw {
		if r.Provider == "internal" {
			continue
		}
		r.Hosts = extractHosts(r.Rule)
		out = append(out, r)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out, nil
}

// hostRulePattern matches Host(`name.tld`) inside a router rule, even
// composed with other matchers (PathPrefix, etc). Same regex shape as the
// compose-label extractor, kept duplicated here so the package stands alone.
var hostRulePattern = regexp.MustCompile("Host\\(`([^`]+)`\\)")

func extractHosts(rule string) []string {
	matches := hostRulePattern.FindAllStringSubmatch(rule, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) > 1 && m[1] != "" {
			out = append(out, m[1])
		}
	}
	return out
}
