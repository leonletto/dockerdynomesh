package render

import (
	"strings"
	"testing"
)

func TestRenderSingleContainerLocalOnly(t *testing.T) {
	cfg := Config{
		Suffixes: []string{"docker.localhost"},
		Containers: []Container{
			{Hostname: "nginx.repo", Address: "172.20.0.5:80"},
		},
	}
	yaml, err := Render(cfg)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := []string{
		"http:",
		"routers:",
		"nginx-repo:",
		"entryPoints:",
		"- websecure",
		"rule: Host(`nginx.repo.docker.localhost`)",
		"service: nginx-repo",
		"tls: {}",
		// companion HTTP redirect router
		"nginx-repo-http:",
		"- web",
		"- redirect-to-https",
		"services:",
		"loadBalancer:",
		"url: http://172.20.0.5:80",
	}
	for _, w := range want {
		if !strings.Contains(yaml, w) {
			t.Errorf("rendered YAML missing %q\n--- output ---\n%s", w, yaml)
		}
	}
	if strings.Contains(yaml, "redirectScheme:") {
		t.Fatalf("rendered auto.yml must not define redirect-to-https; lives in standard-middlewares.yml now\n%s", yaml)
	}
	if !strings.Contains(yaml, "- redirect-to-https") {
		t.Fatalf("HTTP routers must still reference redirect-to-https by name\n%s", yaml)
	}
}

func TestRenderWithTailnetHostname(t *testing.T) {
	cfg := Config{
		Suffixes:      []string{"docker.localhost"},
		MachineName:   "host-mbp",
		TailnetDomain: "tail0123.ts.net",
		Containers: []Container{
			{Hostname: "nginx.repo", Address: "172.20.0.5:80"},
		},
	}
	yaml, err := Render(cfg)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// Remote variant: <hostname>.<machine>.<tailnet> — suffix omitted.
	// MagicDNS routes by machine name; the suffix is local-only.
	// Traefik v3 requires `||` between separate Host() calls (the
	// `Host(\`a\`, \`b\`)` multi-arg form was removed in v3).
	wantRule := "rule: Host(`nginx.repo.docker.localhost`) || Host(`nginx.repo.host-mbp.tail0123.ts.net`)"
	if !strings.Contains(yaml, wantRule) {
		t.Errorf("rendered YAML missing %q\n--- output ---\n%s", wantRule, yaml)
	}
	// Both the websecure router and the web redirect router share the same rule.
	if strings.Count(yaml, wantRule) != 2 {
		t.Errorf("expected rule to appear twice (websecure + web), got %d\n--- output ---\n%s",
			strings.Count(yaml, wantRule), yaml)
	}
	if !strings.Contains(yaml, "nginx-repo-http:") {
		t.Errorf("rendered YAML missing companion web redirect router nginx-repo-http:\n%s", yaml)
	}
}

func TestRenderDeterministicOrder(t *testing.T) {
	// Input order intentionally unsorted; output must be sorted by
	// router name so generated YAML doesn't churn between runs and
	// trigger spurious Traefik reloads.
	cfg := Config{
		Suffixes: []string{"docker.localhost"},
		Containers: []Container{
			{Hostname: "zeta.repo", Address: "172.20.0.7:80"},
			{Hostname: "alpha.repo", Address: "172.20.0.5:80"},
			{Hostname: "mid.repo", Address: "172.20.0.6:80"},
		},
	}
	yaml, err := Render(cfg)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	idxAlpha := strings.Index(yaml, "alpha-repo:")
	idxMid := strings.Index(yaml, "mid-repo:")
	idxZeta := strings.Index(yaml, "zeta-repo:")
	if !(idxAlpha < idxMid && idxMid < idxZeta) {
		t.Errorf("routers not in alphabetical order: alpha=%d mid=%d zeta=%d", idxAlpha, idxMid, idxZeta)
	}
}

func TestRenderRejectsUnsafeHostname(t *testing.T) {
	cases := []string{
		"foo`bar",       // backtick
		"foo,bar",       // comma
		"foo bar",       // space
		"-leadinghyphen", // can't start with hyphen
		"FooUpper",      // uppercase
	}
	for _, h := range cases {
		_, err := Render(Config{
			Suffixes:   []string{"docker.localhost"},
			Containers: []Container{{Hostname: h, Address: "1.2.3.4:80"}},
		})
		if err == nil {
			t.Errorf("expected error for unsafe hostname %q", h)
		}
	}
}

func TestRenderMultipleSuffixes(t *testing.T) {
	cfg := Config{
		Suffixes: []string{"docker.localhost", "orb.local"},
		Containers: []Container{
			{Hostname: "nginx.repo", Address: "10.0.0.1:80"},
		},
	}
	out, err := Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := "rule: Host(`nginx.repo.docker.localhost`) || Host(`nginx.repo.orb.local`)"
	if !strings.Contains(out, want) {
		t.Fatalf("missing rule fragment %q in:\n%s", want, out)
	}
}

func TestRenderMultipleSuffixesWithTailnet(t *testing.T) {
	cfg := Config{
		Suffixes:      []string{"docker.localhost", "orb.local"},
		MachineName:   "host-mbp",
		TailnetDomain: "tail0123.ts.net",
		Containers: []Container{
			{Hostname: "nginx.repo", Address: "10.0.0.1:80"},
		},
	}
	out, err := Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := "rule: Host(`nginx.repo.docker.localhost`) || Host(`nginx.repo.orb.local`) || Host(`nginx.repo.host-mbp.tail0123.ts.net`)"
	if !strings.Contains(out, want) {
		t.Fatalf("missing rule fragment %q in:\n%s", want, out)
	}
}

func TestRenderRejectsInvalidSuffixInList(t *testing.T) {
	cfg := Config{
		Suffixes:   []string{"docker.localhost", "BAD..NAME"},
		Containers: []Container{{Hostname: "nginx.repo", Address: "10.0.0.1:80"}},
	}
	if _, err := Render(cfg); err == nil {
		t.Fatal("expected error for invalid suffix in list")
	}
}

func TestRenderEmptyContainersReturnsValidEmptyYAML(t *testing.T) {
	yaml, err := Render(Config{Suffixes: []string{"docker.localhost"}})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// Traefik wants an empty http: section with both routers: and services:
	// keys present, not a missing one. The template emits both unconditionally.
	for _, want := range []string{"http:", "routers:", "services:"} {
		if !strings.Contains(yaml, want) {
			t.Errorf("expected %q in output\n%s", want, yaml)
		}
	}
}

// TestRenderRedirectRouterEmitted checks that each service gets a companion
// web-entrypoint router that redirects HTTP → HTTPS via the shared middleware.
func TestRenderRedirectRouterEmitted(t *testing.T) {
	cfg := Config{
		Suffixes: []string{"docker.localhost"},
		Containers: []Container{
			{Hostname: "nginx.repo", Address: "10.0.0.1:80"},
		},
	}
	out, err := Render(cfg)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// websecure router
	if !strings.Contains(out, "nginx-repo:") {
		t.Errorf("missing websecure router nginx-repo:\n%s", out)
	}
	// companion web redirect router
	if !strings.Contains(out, "nginx-repo-http:") {
		t.Errorf("missing web redirect router nginx-repo-http:\n%s", out)
	}
	if !strings.Contains(out, "- web") {
		t.Errorf("web redirect router does not reference 'web' entrypoint:\n%s", out)
	}
	if !strings.Contains(out, "- redirect-to-https") {
		t.Errorf("web redirect router does not reference redirect-to-https middleware:\n%s", out)
	}
}

// TestRenderRedirectMiddlewareExactlyOnce ensures the shared redirect-to-https
// middleware is emitted exactly once regardless of how many services are present.
func TestRenderRejectsInvalidMachineName(t *testing.T) {
	cases := []string{"BadMachine", "bad machine", "bad,machine", "-leadinghyphen"}
	for _, m := range cases {
		_, err := Render(Config{
			Suffixes:    []string{"docker.localhost"},
			MachineName: m,
			Containers:  []Container{{Hostname: "nginx.repo", Address: "1.2.3.4:80"}},
		})
		if err == nil {
			t.Errorf("expected error for invalid MachineName %q", m)
		}
	}
}

func TestRenderRejectsInvalidTailnetDomain(t *testing.T) {
	cases := []string{"BAD.Domain", "bad domain", "bad,domain"}
	for _, d := range cases {
		_, err := Render(Config{
			Suffixes:      []string{"docker.localhost"},
			TailnetDomain: d,
			Containers:    []Container{{Hostname: "nginx.repo", Address: "1.2.3.4:80"}},
		})
		if err == nil {
			t.Errorf("expected error for invalid TailnetDomain %q", d)
		}
	}
}

func TestRenderEmptyMachineAndTailnetAreValid(t *testing.T) {
	// Empty MachineName and TailnetDomain are allowed (disables tailnet routes).
	_, err := Render(Config{
		Suffixes:      []string{"docker.localhost"},
		MachineName:   "",
		TailnetDomain: "",
		Containers:    []Container{{Hostname: "nginx.repo", Address: "1.2.3.4:80"}},
	})
	if err != nil {
		t.Errorf("expected no error for empty MachineName/TailnetDomain, got: %v", err)
	}
}

func TestRenderValidMachineAndTailnetPass(t *testing.T) {
	// Positive control: valid MachineName and TailnetDomain should produce no error.
	_, err := Render(Config{
		Suffixes:      []string{"docker.localhost"},
		MachineName:   "host-mbp",
		TailnetDomain: "tail0123.ts.net",
		Containers:    []Container{{Hostname: "nginx.repo", Address: "1.2.3.4:80"}},
	})
	if err != nil {
		t.Errorf("expected no error for valid MachineName/TailnetDomain, got: %v", err)
	}
}

func TestRenderRedirectMiddlewareExactlyOnce(t *testing.T) {
	cfg := Config{
		Suffixes: []string{"docker.localhost"},
		Containers: []Container{
			{Hostname: "alpha.repo", Address: "10.0.0.1:80"},
			{Hostname: "beta.repo", Address: "10.0.0.2:80"},
			{Hostname: "gamma.repo", Address: "10.0.0.3:80"},
		},
	}
	out, err := Render(cfg)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// The middleware definition must NOT appear in rendered output;
	// it lives in standard-middlewares.yml (shipped by T3).
	if strings.Contains(out, "redirectScheme:") {
		t.Fatalf("rendered auto.yml must not define redirect-to-https; lives in standard-middlewares.yml now\n%s", out)
	}
	// Each service still references the middleware by name (3 services → 3 references).
	refCount := strings.Count(out, "- redirect-to-https")
	if refCount != 3 {
		t.Errorf("expected 3 redirect-to-https references (one per service), got %d\n--- output ---\n%s", refCount, out)
	}
}
