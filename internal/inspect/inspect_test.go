package inspect

import (
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
)

func makeContainer(name string, labels map[string]string, networks map[string]*network.EndpointSettings, ports nat.PortSet) container.InspectResponse {
	return container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{
			ID:   "abc123def456",
			Name: "/" + name,
		},
		Config: &container.Config{
			Labels:       labels,
			ExposedPorts: ports,
		},
		NetworkSettings: &container.NetworkSettings{
			Networks: networks,
		},
	}
}

func TestFromContainerCompose(t *testing.T) {
	c := makeContainer("repo-nginx-1",
		map[string]string{
			"com.docker.compose.project": "repo",
			"com.docker.compose.service": "nginx",
		},
		map[string]*network.EndpointSettings{
			DefaultNetworkName: {IPAddress: "172.20.0.5"},
		},
		nat.PortSet{nat.Port("80/tcp"): {}},
	)
	host, addr, ok, err := FromContainer(c, DefaultNetworkName)
	if err != nil {
		t.Fatalf("FromContainer: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if host != "nginx.repo" {
		t.Errorf("hostname = %q; want %q", host, "nginx.repo")
	}
	if addr != "172.20.0.5:80" {
		t.Errorf("address = %q; want %q", addr, "172.20.0.5:80")
	}
}

func TestFromContainerSkipsTraefikLabeled(t *testing.T) {
	c := makeContainer("foo",
		map[string]string{"traefik.http.routers.foo.rule": "Host(`foo.example.com`)"},
		map[string]*network.EndpointSettings{DefaultNetworkName: {IPAddress: "172.20.0.5"}},
		nat.PortSet{nat.Port("80/tcp"): {}},
	)
	_, _, ok, err := FromContainer(c, DefaultNetworkName)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if ok {
		t.Error("expected skip for traefik-labeled container")
	}
}

func TestFromContainerSkipsNotOnNetwork(t *testing.T) {
	c := makeContainer("foo", nil,
		map[string]*network.EndpointSettings{"some-other-net": {IPAddress: "172.20.0.5"}},
		nat.PortSet{nat.Port("80/tcp"): {}},
	)
	_, _, ok, err := FromContainer(c, DefaultNetworkName)
	if err != nil || ok {
		t.Errorf("expected skip for off-network container; ok=%v err=%v", ok, err)
	}
}

func TestFromContainerSkipsNoUsablePort(t *testing.T) {
	c := makeContainer("foo", nil,
		map[string]*network.EndpointSettings{DefaultNetworkName: {IPAddress: "172.20.0.5"}},
		nat.PortSet{nat.Port("9999/tcp"): {}, nat.Port("8888/tcp"): {}},
	)
	_, _, ok, err := FromContainer(c, DefaultNetworkName)
	if err != nil || ok {
		t.Errorf("expected skip for no-usable-port container; ok=%v err=%v", ok, err)
	}
}

func TestFromContainerStandalone(t *testing.T) {
	c := makeContainer("step-ca", nil,
		map[string]*network.EndpointSettings{DefaultNetworkName: {IPAddress: "172.20.0.6"}},
		nat.PortSet{nat.Port("8443/tcp"): {}},
	)
	host, addr, ok, err := FromContainer(c, DefaultNetworkName)
	if err != nil || !ok {
		t.Fatalf("expected ok; ok=%v err=%v", ok, err)
	}
	if host != "step-ca" {
		t.Errorf("hostname = %q; want %q", host, "step-ca")
	}
	if addr != "172.20.0.6:8443" {
		t.Errorf("address = %q; want %q", addr, "172.20.0.6:8443")
	}
}

func TestFromContainerLabelOverridesPort(t *testing.T) {
	c := makeContainer("foo",
		map[string]string{PortLabel: "9000"},
		map[string]*network.EndpointSettings{DefaultNetworkName: {IPAddress: "172.20.0.5"}},
		nat.PortSet{nat.Port("80/tcp"): {}, nat.Port("9000/tcp"): {}},
	)
	_, addr, ok, err := FromContainer(c, DefaultNetworkName)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if addr != "172.20.0.5:9000" {
		t.Errorf("address = %q; want :9000", addr)
	}
}

func TestFromContainerNilConfigSkips(t *testing.T) {
	c := container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{ID: "abc123def456", Name: "/foo"},
		Config:            nil,
		NetworkSettings: &container.NetworkSettings{
			Networks: map[string]*network.EndpointSettings{DefaultNetworkName: {IPAddress: "172.20.0.5"}},
		},
	}
	_, _, ok, err := FromContainer(c, DefaultNetworkName)
	if err != nil || ok {
		t.Errorf("expected skip for nil Config; ok=%v err=%v", ok, err)
	}
}

func TestFromContainerLowercasesHostname(t *testing.T) {
	// Containers with uppercase in their name must be lowercased before the
	// charset check in render.Render so they are accepted (RFC 1123 §2.1:
	// hostnames are case-insensitive).
	c := makeContainer("MyApp", nil,
		map[string]*network.EndpointSettings{DefaultNetworkName: {IPAddress: "172.20.0.9"}},
		nat.PortSet{nat.Port("80/tcp"): {}},
	)
	host, _, ok, err := FromContainer(c, DefaultNetworkName)
	if err != nil || !ok {
		t.Fatalf("expected ok; ok=%v err=%v", ok, err)
	}
	if host != "myapp" {
		t.Errorf("hostname = %q; want %q", host, "myapp")
	}
}

func TestFromContainerLowercasesComposeHostname(t *testing.T) {
	// Compose labels with uppercase project/service must also be lowercased.
	c := makeContainer("MyProject-MyService-1",
		map[string]string{
			"com.docker.compose.project": "MyProject",
			"com.docker.compose.service": "MyService",
		},
		map[string]*network.EndpointSettings{DefaultNetworkName: {IPAddress: "172.20.0.10"}},
		nat.PortSet{nat.Port("80/tcp"): {}},
	)
	host, _, ok, err := FromContainer(c, DefaultNetworkName)
	if err != nil || !ok {
		t.Fatalf("expected ok; ok=%v err=%v", ok, err)
	}
	if host != "myservice.myproject" {
		t.Errorf("hostname = %q; want %q", host, "myservice.myproject")
	}
}

func TestLabeledMissingNetwork(t *testing.T) {
	mk := func(labels map[string]string, networks ...string) container.InspectResponse {
		nets := map[string]*network.EndpointSettings{}
		for _, n := range networks {
			nets[n] = &network.EndpointSettings{IPAddress: "10.0.0.2"}
		}
		return container.InspectResponse{
			ContainerJSONBase: &container.ContainerJSONBase{Name: "/test", ID: "abc123"},
			Config:            &container.Config{Labels: labels},
			NetworkSettings:   &container.NetworkSettings{Networks: nets},
		}
	}

	cases := []struct {
		name           string
		c              container.InspectResponse
		want           bool
		wantAttachedAt int // len of attached
	}{
		{
			name:           "no traefik labels — not flagged",
			c:              mk(map[string]string{}, "app-net"),
			want:           false,
			wantAttachedAt: 0,
		},
		{
			name:           "labeled and on dynomesh-net — not flagged",
			c:              mk(map[string]string{"traefik.enable": "true"}, "dynomesh-net"),
			want:           false,
			wantAttachedAt: 0,
		},
		{
			name:           "labeled but only on app-net — flagged",
			c:              mk(map[string]string{"traefik.enable": "true"}, "app-net"),
			want:           true,
			wantAttachedAt: 1,
		},
		{
			name: "labeled with traefik.docker.network override pointing at attached net — not flagged",
			c: mk(map[string]string{
				"traefik.enable":         "true",
				"traefik.docker.network": "app-net",
			}, "app-net"),
			want:           false,
			wantAttachedAt: 0,
		},
		{
			name: "labeled with traefik.docker.network override pointing at unattached net — flagged",
			c: mk(map[string]string{
				"traefik.enable":         "true",
				"traefik.docker.network": "some-other-net",
			}, "app-net"),
			want:           true,
			wantAttachedAt: 1,
		},
		{
			name: "nil Config — not flagged",
			c: container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{Name: "/test", ID: "abc123"},
				Config:            nil,
				NetworkSettings:   &container.NetworkSettings{Networks: map[string]*network.EndpointSettings{"app-net": {IPAddress: "10.0.0.2"}}},
			},
			want:           false,
			wantAttachedAt: 0,
		},
		{
			name: "nil NetworkSettings — not flagged",
			c: container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{Name: "/test", ID: "abc123"},
				Config:            &container.Config{Labels: map[string]string{"traefik.enable": "true"}},
				NetworkSettings:   nil,
			},
			want:           false,
			wantAttachedAt: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			attached, got := LabeledMissingNetwork(tc.c, "dynomesh-net")
			if got != tc.want {
				t.Fatalf("misconfigured=%v want %v (attached=%v)", got, tc.want, attached)
			}
			if got && len(attached) != tc.wantAttachedAt {
				t.Fatalf("attached len=%d want %d (attached=%v)", len(attached), tc.wantAttachedAt, attached)
			}
		})
	}
}

func TestProjectsOfDeduplicatesAndSorts(t *testing.T) {
	cs := []container.InspectResponse{
		makeContainer("a", map[string]string{"com.docker.compose.project": "zeta"}, nil, nil),
		makeContainer("b", map[string]string{"com.docker.compose.project": "alpha"}, nil, nil),
		makeContainer("c", map[string]string{"com.docker.compose.project": "alpha"}, nil, nil),
		makeContainer("d", nil, nil, nil),
	}
	got := ProjectsOf(cs)
	want := []string{"alpha", "zeta"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("ProjectsOf = %v; want %v", got, want)
	}
}
