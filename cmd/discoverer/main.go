// Discoverer watches Docker events and writes Traefik dynamic config.
// On startup it does a full container scan and submits the project set
// to certgen, waits for the cert reissue response, then writes the
// dynamic config. Subsequent events trigger a debounced reconcile.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/docker/docker/client"
	"github.com/leonletto/dockerdynomesh/internal/certclient"
	"github.com/leonletto/dockerdynomesh/internal/certgen"
	"github.com/leonletto/dockerdynomesh/internal/inspect"
	"github.com/leonletto/dockerdynomesh/internal/render"
)

func main() {
	var (
		suffix      = flag.String("suffix", envOr("SUFFIX", "docker.localhost"), "hostname suffix")
		machine     = flag.String("machine", os.Getenv("MACHINE_NAME"), "Tailscale machine short name")
		tailnet     = flag.String("tailnet", os.Getenv("TAILNET_DOMAIN"), "Tailscale tailnet domain")
		dynamicPath = flag.String("out", "/shared/dynamic/auto.yml", "output Traefik dynamic file")
		certgenSock = flag.String("certgen", "/shared/run/certgen.sock", "certgen Unix socket path")
		debounceMS  = flag.Int("debounce-ms", envOrInt("DEBOUNCE_MS", 500), "event debounce in ms")
	)
	flag.Parse()

	suffixes, err := parseTakeoverSuffixes(*suffix, os.Getenv("TAKEOVER_SUFFIXES"))
	if err != nil {
		log.Fatalf("parse takeover suffixes: %v", err)
	}

	dc, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("docker client: %v", err)
	}
	defer dc.Close()

	cg := certclient.New(*certgenSock)

	networkName := envOr("NETWORK_NAME", inspect.DefaultNetworkName)

	r := &reconciler{
		docker:          dc,
		certgen:         cg,
		render:          func(cfg render.Config) (string, error) { return render.Render(cfg) },
		writeOut:        atomicWrite,
		out:             *dynamicPath,
		suffixes:        suffixes,
		machine:         *machine,
		tailnet:         *tailnet,
		network:         networkName,
		debounce:        time.Duration(*debounceMS) * time.Millisecond,
		loggedMisconfig: map[string]string{},
	}

	// Startup banner — one line with the resolved config so operators
	// can immediately confirm what the process is using.
	machineName := *machine
	if machineName == "" {
		machineName = "<none>"
	}
	tailnetDomain := *tailnet
	if tailnetDomain == "" {
		tailnetDomain = "<none>"
	}
	takeoverStr := strings.Join(suffixes, ",")
	if takeoverStr == "" {
		takeoverStr = "<none>"
	}
	log.Printf("boot: suffix=%s machine=%s tailnet=%s takeover_suffixes=%s out=%s certgen=%s network=%s",
		suffixes[0], machineName, tailnetDomain, takeoverStr, *dynamicPath, *certgenSock, networkName)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
		<-c
		cancel()
	}()

	if err := r.run(ctx); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envOrInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// certReissuer is the subset of certclient.Client the reconciler needs.
// Defined as an interface so reconcile() can be unit-tested with a fake.
type certReissuer interface {
	Reissue(ctx context.Context, req certgen.ReissueRequest) (bool, []string, error)
}

// reconciler is wired up in reconcile.go.
type reconciler struct {
	docker       *client.Client
	certgen      certReissuer
	render       func(render.Config) (string, error)
	writeOut     func(path, content string) error
	out          string
	suffixes     []string
	machine      string
	tailnet      string
	network      string
	debounce     time.Duration
	lastProjects []string
	// bootMaxAttempts and bootRetryInterval control the initial-reconcile
	// retry loop. Zero values use the defaults (10 attempts, 500ms apart).
	bootMaxAttempts   int
	bootRetryInterval time.Duration
	// reconcileFn is called by bootReconcile. Nil means use r.reconcile.
	// Tests inject a stub here to drive the retry loop without docker.
	reconcileFn func(ctx context.Context) error
	// loggedMisconfig tracks per-container fingerprints of attached
	// network sets we've already warned about, so we don't spam on every
	// docker event for the same container. Keyed by container ID; value
	// is the joined sorted network names. A change in the set triggers a
	// fresh log line on the next reconcile.
	loggedMisconfig map[string]string
}

// parseTakeoverSuffixes returns the full ordered suffix list (sorted, deduped)
// derived from the canonical primary suffix and the comma-separated env value.
// Each entry is validated via certgen.ValidateLabel.
func parseTakeoverSuffixes(primary, env string) ([]string, error) {
	if !certgen.ValidateLabel(primary) {
		return nil, fmt.Errorf("invalid primary suffix: %q", primary)
	}
	seen := map[string]struct{}{primary: {}}
	out := []string{primary}
	for raw := range strings.SplitSeq(env, ",") {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if !certgen.ValidateLabel(s) {
			return nil, fmt.Errorf("invalid takeover suffix: %q", s)
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out, nil
}

