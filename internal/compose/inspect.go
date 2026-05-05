package compose

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Inspection is a read-only view of a docker-compose file — what the
// declarative file says, not what's running. Consumers (UI, API) use it
// to render ports and traefik routes per service without parsing YAML
// themselves.
type Inspection struct {
	Services []ServiceInfo
}

// ServiceInfo is one service block from the compose file. Image, ports,
// and traefik routes are the only fields we extract today; add more as the
// UI needs them.
type ServiceInfo struct {
	Name   string
	Image  string
	Ports  []PortMapping
	Routes []Route
}

// PortMapping mirrors a single entry in services.X.ports. Compose ports
// have many forms; we render them as a single user-facing string and keep
// the structured fields for future filtering.
type PortMapping struct {
	// Display is the short form a UI can render — e.g. "80:80/tcp" or
	// "127.0.0.1:8080:80". Always populated.
	Display string

	Host      string // e.g. "127.0.0.1"; empty if not pinned
	Published string // host port — string to support ranges + named ports
	Target    string // container port
	Protocol  string // "tcp" / "udp"; empty defaults to tcp
}

// Route is one traefik HTTP router we managed to extract from the labels.
// URL is "scheme://host" — concatenate path bits when we add path-based
// support later.
type Route struct {
	Router string // router name (the `R` in traefik.http.routers.R.*)
	Host   string
	Scheme string // "http" / "https"
	URL    string // "scheme://host" — the convenience field UIs link to
}

// AllRoutes flattens routes across every service into a single slice,
// ordered by host. Used by the index page where the per-service split
// doesn't matter.
func (i Inspection) AllRoutes() []Route {
	var out []Route
	for _, s := range i.Services {
		out = append(out, s.Routes...)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Host < out[b].Host })
	return out
}

// inspectFile is the on-disk shape of compose used by Inspect. It's a
// superset of what other extractors here need — labels, ports, image —
// so we keep it private and re-derive at consumption time.
type inspectFile struct {
	Services map[string]inspectService `yaml:"services"`
}

type inspectService struct {
	Image  string    `yaml:"image"`
	Ports  yaml.Node `yaml:"ports"`
	Labels LabelMap  `yaml:"labels"`
}

// Inspect reads a docker-compose file and returns the structured view.
// Services come back ordered by name so the UI render is stable.
//
// Unparseable port entries are silently skipped rather than failing the
// whole call — a malformed `ports: [oops]` shouldn't blank the entire
// services panel.
func Inspect(path string) (Inspection, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Inspection{}, fmt.Errorf("read compose file %s: %w", path, err)
	}

	var parsed inspectFile
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return Inspection{}, fmt.Errorf("parse compose file %s: %w", path, err)
	}

	names := make([]string, 0, len(parsed.Services))
	for name := range parsed.Services {
		names = append(names, name)
	}
	sort.Strings(names)

	services := make([]ServiceInfo, 0, len(names))
	for _, name := range names {
		raw := parsed.Services[name]
		services = append(services, ServiceInfo{
			Name:   name,
			Image:  raw.Image,
			Ports:  parsePorts(raw.Ports),
			Routes: parseTraefikRoutes(raw.Labels),
		})
	}

	return Inspection{Services: services}, nil
}

// parsePorts handles compose's polymorphic ports field — short string
// (`"80:80"`, `"127.0.0.1:80:80"`, `"80:80/tcp"`) or long form
// (`{ target: 80, published: 80, protocol: tcp }`). Range syntax
// (`"80-90:80-90"`) is preserved verbatim in Display but not split into
// individual mappings.
func parsePorts(node yaml.Node) []PortMapping {
	if node.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]PortMapping, 0, len(node.Content))
	for _, item := range node.Content {
		switch item.Kind {
		case yaml.ScalarNode:
			if pm, ok := parsePortString(item.Value); ok {
				out = append(out, pm)
			}
		case yaml.MappingNode:
			pm, ok := parsePortMapping(item)
			if ok {
				out = append(out, pm)
			}
		}
	}
	return out
}

// parsePortString handles the short-form variants. The grammar is
// "[host:]published:target[/protocol]"; published may be omitted ("80")
// for container-internal ports.
func parsePortString(raw string) (PortMapping, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return PortMapping{}, false
	}
	display := value

	protocol := ""
	if idx := strings.LastIndex(value, "/"); idx >= 0 {
		protocol = value[idx+1:]
		value = value[:idx]
	}

	parts := strings.Split(value, ":")
	pm := PortMapping{Display: display, Protocol: protocol}
	switch len(parts) {
	case 1:
		pm.Target = parts[0]
	case 2:
		pm.Published = parts[0]
		pm.Target = parts[1]
	case 3:
		pm.Host = parts[0]
		pm.Published = parts[1]
		pm.Target = parts[2]
	default:
		return PortMapping{}, false
	}
	return pm, true
}

func parsePortMapping(node *yaml.Node) (PortMapping, bool) {
	pm := PortMapping{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		val := node.Content[i+1].Value
		switch key {
		case "target":
			pm.Target = val
		case "published":
			pm.Published = val
		case "protocol":
			pm.Protocol = val
		case "host_ip":
			pm.Host = val
		}
	}
	if pm.Target == "" {
		return PortMapping{}, false
	}
	pm.Display = renderPortDisplay(pm)
	return pm, true
}

func renderPortDisplay(pm PortMapping) string {
	var b strings.Builder
	if pm.Host != "" {
		b.WriteString(pm.Host)
		b.WriteByte(':')
	}
	if pm.Published != "" {
		b.WriteString(pm.Published)
		b.WriteByte(':')
	}
	b.WriteString(pm.Target)
	if pm.Protocol != "" && pm.Protocol != "tcp" {
		b.WriteByte('/')
		b.WriteString(pm.Protocol)
	}
	return b.String()
}

// hostRulePattern matches Host(`name.tld`) inside a traefik router rule,
// even when combined with other matchers (`Host(`a.com`) && PathPrefix(`/api`)`,
// `Host(`a.com`) || Host(`b.com`)`). Backtick-delimited because that's
// what traefik mandates around hostnames.
var hostRulePattern = regexp.MustCompile("Host\\(`([^`]+)`\\)")

// parseTraefikRoutes extracts routable URLs from labels. Requires
// traefik.enable=true; rules without a parsable Host() are skipped (no
// guess for HostRegexp / Path-only routers). Multiple Host() in one rule
// produce multiple Route entries with the same router name.
func parseTraefikRoutes(labels LabelMap) []Route {
	if labels == nil {
		return nil
	}
	if v, ok := labels["traefik.enable"]; !ok || strings.ToLower(strings.TrimSpace(v)) != "true" {
		return nil
	}

	type routerCfg struct {
		hosts       []string
		entrypoints string
		tls         bool
	}
	routers := map[string]*routerCfg{}
	getRouter := func(name string) *routerCfg {
		r, ok := routers[name]
		if !ok {
			r = &routerCfg{}
			routers[name] = r
		}
		return r
	}

	const (
		prefix    = "traefik.http.routers."
		ruleSfx   = ".rule"
		entrySfx  = ".entrypoints"
		tlsSfx    = ".tls"
	)

	for key, val := range labels {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		rest := strings.TrimPrefix(key, prefix)
		switch {
		case strings.HasSuffix(rest, ruleSfx):
			name := strings.TrimSuffix(rest, ruleSfx)
			matches := hostRulePattern.FindAllStringSubmatch(val, -1)
			for _, m := range matches {
				if len(m) > 1 && m[1] != "" {
					getRouter(name).hosts = append(getRouter(name).hosts, m[1])
				}
			}
		case strings.HasSuffix(rest, entrySfx):
			name := strings.TrimSuffix(rest, entrySfx)
			getRouter(name).entrypoints = val
		case strings.HasSuffix(rest, tlsSfx):
			name := strings.TrimSuffix(rest, tlsSfx)
			getRouter(name).tls = strings.ToLower(strings.TrimSpace(val)) == "true"
		}
	}

	names := make([]string, 0, len(routers))
	for n := range routers {
		names = append(names, n)
	}
	sort.Strings(names)

	var out []Route
	for _, name := range names {
		r := routers[name]
		scheme := schemeFor(r.entrypoints, r.tls)
		for _, host := range r.hosts {
			out = append(out, Route{
				Router: name,
				Host:   host,
				Scheme: scheme,
				URL:    scheme + "://" + host,
			})
		}
	}
	return out
}

// schemeFor picks http or https based on traefik's entrypoint convention
// (entrypoint name containing "secure", or explicit tls=true). When the
// entrypoint is missing we default to http — most local-dev sandboxes
// don't set it explicitly.
func schemeFor(entrypoints string, tls bool) string {
	if tls {
		return "https"
	}
	for _, ep := range strings.Split(entrypoints, ",") {
		if strings.Contains(strings.ToLower(strings.TrimSpace(ep)), "secure") {
			return "https"
		}
	}
	return "http"
}
