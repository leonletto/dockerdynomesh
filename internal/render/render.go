// Package render generates Traefik dynamic configuration from
// observed Docker container state.
package render

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"text/template"
)

// hostnameRE limits hostname characters to those safe inside a Traefik
// Host(`...`) rule — no backticks, no commas, no whitespace. Adversarial
// container labels could otherwise inject extra rules.
var hostnameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`)

// Container is the discoverer-supplied view of a container worth
// routing to. Project is the compose project name (empty if not a
// compose container). Service is the compose service name (empty
// if not a compose container). Name is the container name.
// Hostname is the inferred routing base (e.g. "nginx.repo" for a
// compose service or "step-ca" for a standalone container).
// Address is the form "ip:port" that Traefik will forward to.
type Container struct {
	Project  string
	Service  string
	Name     string
	Hostname string
	Address  string
}

// Config is the input to Render: every container the discoverer
// wants Traefik to route to, plus the suffix list and (optional)
// machine/tailnet identifiers that produce the cross-machine variant.
// Suffixes is non-empty; each entry is charset-validated. The first
// entry is the canonical suffix, but Render sorts the list before
// emitting rules so output is byte-stable.
type Config struct {
	Suffixes      []string // e.g. {"docker.localhost"} or {"docker.localhost","orb.local"}
	MachineName   string   // e.g. "host-mbp" — empty disables tailnet hostnames
	TailnetDomain string   // e.g. "tail0123.ts.net" — empty disables tailnet hostnames
	Containers    []Container
}

const tmpl = `http:
  routers:
{{- range .Routers }}
    {{ .Name }}:
      entryPoints:
        - websecure
      rule: {{ .Rule }}
      service: {{ .Name }}
      tls: {}
    {{ .Name }}-http:
      entryPoints:
        - web
      rule: {{ .Rule }}
      service: {{ .Name }}
      middlewares:
        - redirect-to-https
{{- end }}
  services:
{{- range .Routers }}
    {{ .Name }}:
      loadBalancer:
        servers:
          - url: http://{{ .Address }}
{{- end }}
`

type renderRouter struct {
	Name    string
	Rule    string
	Address string
}

type renderData struct {
	Routers []renderRouter
}

// Render emits Traefik dynamic-config YAML from cfg. Hostnames in
// cfg.Containers may include dots (e.g. "nginx.repo"); Render
// converts them to a router-name-safe form by replacing dots with
// hyphens. The Host(...) rule is built by appending cfg.Suffix
// (and the tailnet variant if MachineName and TailnetDomain are set).
func Render(cfg Config) (string, error) {
	if len(cfg.Suffixes) == 0 {
		return "", fmt.Errorf("Suffixes is required")
	}
	suffixes := append([]string(nil), cfg.Suffixes...)
	sort.Strings(suffixes)
	for _, s := range suffixes {
		if !hostnameRE.MatchString(s) {
			return "", fmt.Errorf("unsafe suffix %q: must match %s", s, hostnameRE)
		}
	}
	if cfg.MachineName != "" && !hostnameRE.MatchString(cfg.MachineName) {
		return "", fmt.Errorf("unsafe MachineName %q: must match %s", cfg.MachineName, hostnameRE)
	}
	if cfg.TailnetDomain != "" && !hostnameRE.MatchString(cfg.TailnetDomain) {
		return "", fmt.Errorf("unsafe TailnetDomain %q: must match %s", cfg.TailnetDomain, hostnameRE)
	}
	var data renderData
	for _, c := range cfg.Containers {
		if c.Hostname == "" || c.Address == "" {
			return "", fmt.Errorf("container missing Hostname or Address: %+v", c)
		}
		if !hostnameRE.MatchString(c.Hostname) {
			return "", fmt.Errorf("unsafe hostname %q: must match %s", c.Hostname, hostnameRE)
		}
		name := strings.ReplaceAll(c.Hostname, ".", "-")
		hosts := make([]string, 0, len(suffixes)+1)
		for _, s := range suffixes {
			hosts = append(hosts, fmt.Sprintf("Host(`%s.%s`)", c.Hostname, s))
		}
		if cfg.MachineName != "" && cfg.TailnetDomain != "" {
			hosts = append(hosts, fmt.Sprintf("Host(`%s.%s.%s`)",
				c.Hostname, cfg.MachineName, cfg.TailnetDomain))
		}
		// Traefik v3 dropped the Host(`a`, `b`) multi-arg form; use
		// the explicit `||` matcher composition instead.
		data.Routers = append(data.Routers, renderRouter{
			Name:    name,
			Rule:    strings.Join(hosts, " || "),
			Address: c.Address,
		})
	}
	sort.Slice(data.Routers, func(i, j int) bool {
		return data.Routers[i].Name < data.Routers[j].Name
	})
	t, err := template.New("traefik").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
