// Package inspect derives Traefik routing data from a Docker container's
// metadata. It is the boundary between the Docker SDK and the render
// package: discoverer fetches containers, converts each to a render.Container
// via this package, and passes the slice to render.Render.
package inspect

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types/container"
)

// DefaultNetworkName is the default Docker network discoverer joins when
// NETWORK_NAME is not set.
const DefaultNetworkName = "dynomesh-net"

// PortLabel lets a container override port selection when its image
// exposes more than one port. Value: numeric port (e.g. "8080").
const PortLabel = "dynomesh.port"

// FromContainer derives a routing entry. Returns ok=false when the container
// should be skipped (no IP on the given network, no usable port, or it has
// explicit traefik.* labels meaning the docker provider owns it).
//
// network is the Docker network name to look up (e.g. "dynomesh-net").
// hostname is the routing base (e.g. "nginx.repo" or "step-ca").
// address is "ip:port".
func FromContainer(c container.InspectResponse, network string) (hostname, address string, ok bool, err error) {
	// Partial-state containers can have nil Config. Skip cleanly.
	if c.Config == nil {
		return "", "", false, nil
	}
	// Skip if any traefik.* label is present — Traefik's docker provider handles those.
	for k := range c.Config.Labels {
		if strings.HasPrefix(k, "traefik.") {
			return "", "", false, nil
		}
	}

	ip := networkIP(c, network)
	if ip == "" {
		return "", "", false, nil // not on the configured network
	}

	port, ok := selectPort(c)
	if !ok {
		return "", "", false, nil
	}

	hostname = inferHostname(c)
	if hostname == "" {
		shortID := c.ID
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}
		return "", "", false, fmt.Errorf("container %s has no usable name", shortID)
	}

	return hostname, fmt.Sprintf("%s:%d", ip, port), true, nil
}

func networkIP(c container.InspectResponse, network string) string {
	if c.NetworkSettings == nil {
		return ""
	}
	ep, ok := c.NetworkSettings.Networks[network]
	if !ok || ep == nil {
		return ""
	}
	return ep.IPAddress
}

// selectPort chooses which port to forward to. Order:
//  1. dynomesh.port label
//  2. single exposed port
//  3. one of 80, 8080, 8000 (in that order)
func selectPort(c container.InspectResponse) (int, bool) {
	if v := c.Config.Labels[PortLabel]; v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 && p < 65536 {
			return p, true
		}
	}
	exposed := make([]int, 0, len(c.Config.ExposedPorts))
	for p := range c.Config.ExposedPorts {
		if n, err := strconv.Atoi(p.Port()); err == nil {
			exposed = append(exposed, n)
		}
	}
	sort.Ints(exposed)
	if len(exposed) == 1 {
		return exposed[0], true
	}
	for _, fav := range []int{80, 8080, 8000} {
		for _, p := range exposed {
			if p == fav {
				return fav, true
			}
		}
	}
	return 0, false
}

func inferHostname(c container.InspectResponse) string {
	project := c.Config.Labels["com.docker.compose.project"]
	service := c.Config.Labels["com.docker.compose.service"]
	var h string
	if project != "" && service != "" {
		h = fmt.Sprintf("%s.%s", service, project)
	} else {
		h = strings.TrimPrefix(c.Name, "/")
	}
	return strings.ToLower(h)
}

// ProjectsOf returns the distinct compose project names from the given
// containers, sorted, suitable for the certgen reissue request.
func ProjectsOf(cs []container.InspectResponse) []string {
	seen := map[string]struct{}{}
	for _, c := range cs {
		if c.Config == nil {
			continue
		}
		p := c.Config.Labels["com.docker.compose.project"]
		if p != "" {
			seen[p] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
