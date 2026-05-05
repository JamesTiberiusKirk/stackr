package compose

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeCompose drops a compose file in a tmp dir and returns its path —
// every test starts with a fresh file so they're independent and can run
// in parallel.
func writeCompose(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "docker-compose.yml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

func TestInspect_PortsShortForm(t *testing.T) {
	path := writeCompose(t, `services:
  web:
    image: nginx:alpine
    ports:
      - "8080:80"
      - "127.0.0.1:8443:443"
      - "5353:53/udp"
`)
	got, err := Inspect(path)
	require.NoError(t, err)
	require.Len(t, got.Services, 1)
	ports := got.Services[0].Ports
	require.Len(t, ports, 3)

	require.Equal(t, "8080:80", ports[0].Display)
	require.Equal(t, "8080", ports[0].Published)
	require.Equal(t, "80", ports[0].Target)

	require.Equal(t, "127.0.0.1", ports[1].Host)
	require.Equal(t, "8443", ports[1].Published)
	require.Equal(t, "443", ports[1].Target)

	require.Equal(t, "udp", ports[2].Protocol,
		"the /protocol suffix on a short-form port must round-trip — UDP services are easy to miss otherwise")
}

func TestInspect_PortsLongForm(t *testing.T) {
	path := writeCompose(t, `services:
  web:
    image: nginx:alpine
    ports:
      - target: 80
        published: "8080"
        protocol: tcp
      - target: 443
        published: "8443"
        protocol: tcp
        host_ip: "127.0.0.1"
`)
	got, err := Inspect(path)
	require.NoError(t, err)
	require.Len(t, got.Services[0].Ports, 2)

	require.Equal(t, "8080:80", got.Services[0].Ports[0].Display)
	require.Equal(t, "127.0.0.1:8443:443", got.Services[0].Ports[1].Display)
}

func TestInspect_TraefikSingleHostRule(t *testing.T) {
	path := writeCompose(t, `services:
  web:
    image: nginx:alpine
    labels:
      traefik.enable: "true"
      traefik.http.routers.nginx.rule: "Host(`+"`"+`nginx.localhost`+"`"+`)"
      traefik.http.routers.nginx.entrypoints: "web"
`)
	got, err := Inspect(path)
	require.NoError(t, err)
	require.Len(t, got.Services[0].Routes, 1)
	r := got.Services[0].Routes[0]
	require.Equal(t, "nginx.localhost", r.Host)
	require.Equal(t, "http", r.Scheme)
	require.Equal(t, "http://nginx.localhost", r.URL)
}

func TestInspect_TraefikMultiHostRule(t *testing.T) {
	// Pinned because it's easy to write a regex that grabs only the first
	// Host() per rule. Multiple Host()s in one rule must produce multiple
	// Route entries.
	path := writeCompose(t, `services:
  web:
    image: nginx:alpine
    labels:
      traefik.enable: "true"
      traefik.http.routers.demo.rule: "Host(`+"`"+`a.local`+"`"+`) || Host(`+"`"+`b.local`+"`"+`)"
`)
	got, err := Inspect(path)
	require.NoError(t, err)
	hosts := make([]string, 0, len(got.Services[0].Routes))
	for _, r := range got.Services[0].Routes {
		hosts = append(hosts, r.Host)
	}
	require.ElementsMatch(t, []string{"a.local", "b.local"}, hosts)
}

func TestInspect_TraefikWebsecureBecomesHTTPS(t *testing.T) {
	path := writeCompose(t, `services:
  web:
    image: nginx:alpine
    labels:
      traefik.enable: "true"
      traefik.http.routers.api.rule: "Host(`+"`"+`api.example.com`+"`"+`)"
      traefik.http.routers.api.entrypoints: "websecure"
`)
	got, err := Inspect(path)
	require.NoError(t, err)
	require.Len(t, got.Services[0].Routes, 1)
	require.Equal(t, "https", got.Services[0].Routes[0].Scheme,
		"any entrypoint containing 'secure' must imply https — that's the convention every stackr stack follows")
}

func TestInspect_TraefikIgnoredWithoutEnable(t *testing.T) {
	// Half-configured traefik labels (missing `traefik.enable`) shouldn't
	// produce phantom routes — those services aren't actually exposed.
	path := writeCompose(t, `services:
  web:
    image: nginx:alpine
    labels:
      traefik.http.routers.x.rule: "Host(`+"`"+`x.local`+"`"+`)"
`)
	got, err := Inspect(path)
	require.NoError(t, err)
	require.Empty(t, got.Services[0].Routes)
}

func TestInspect_TraefikIgnoredWhenEnableFalse(t *testing.T) {
	path := writeCompose(t, `services:
  web:
    image: nginx:alpine
    labels:
      traefik.enable: "false"
      traefik.http.routers.x.rule: "Host(`+"`"+`x.local`+"`"+`)"
`)
	got, err := Inspect(path)
	require.NoError(t, err)
	require.Empty(t, got.Services[0].Routes)
}

func TestInspect_NoTraefikLabelsLeavesRoutesEmpty(t *testing.T) {
	path := writeCompose(t, `services:
  web:
    image: nginx:alpine
`)
	got, err := Inspect(path)
	require.NoError(t, err)
	require.Empty(t, got.Services[0].Routes)
	require.Empty(t, got.Services[0].Ports)
}

func TestInspect_ServicesSortedByName(t *testing.T) {
	// Stable iteration order matters for the UI — without it, the panel
	// order on /stacks/{name} flickers between requests because Go maps
	// iterate randomly.
	path := writeCompose(t, `services:
  zebra:
    image: alpine
  alpha:
    image: alpine
  middle:
    image: alpine
`)
	got, err := Inspect(path)
	require.NoError(t, err)
	require.Equal(t, []string{"alpha", "middle", "zebra"},
		[]string{got.Services[0].Name, got.Services[1].Name, got.Services[2].Name})
}

func TestInspect_AllRoutesAggregates(t *testing.T) {
	path := writeCompose(t, `services:
  web:
    image: nginx
    labels:
      traefik.enable: "true"
      traefik.http.routers.web.rule: "Host(`+"`"+`web.local`+"`"+`)"
  api:
    image: nginx
    labels:
      traefik.enable: "true"
      traefik.http.routers.api.rule: "Host(`+"`"+`api.local`+"`"+`)"
`)
	got, err := Inspect(path)
	require.NoError(t, err)
	all := got.AllRoutes()
	require.Len(t, all, 2)
	require.Equal(t, "api.local", all[0].Host)
	require.Equal(t, "web.local", all[1].Host)
}

func TestInspect_MissingFile(t *testing.T) {
	_, err := Inspect("/no/such/file.yml")
	require.Error(t, err)
}
