package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/leonletto/dockerdynomesh/internal/certgen"
	"github.com/leonletto/dockerdynomesh/internal/inspect"
	"github.com/leonletto/dockerdynomesh/internal/render"
)

// bootReconcile runs the initial reconcile with a bounded retry loop.
// It returns only when the reconcile succeeds, all attempts are exhausted,
// or ctx is cancelled. Callers proceed to event subscription regardless of
// the outcome to avoid deadlocking the daemon.
//
// reconcileFn defaults to r.reconcile; tests may inject a stub via that field.
func (r *reconciler) bootReconcile(ctx context.Context) {
	fn := r.reconcileFn
	if fn == nil {
		fn = r.reconcile
	}
	maxAttempts := r.bootMaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 10
	}
	retryInterval := r.bootRetryInterval
	if retryInterval <= 0 {
		retryInterval = 500 * time.Millisecond
	}
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := fn(ctx); err != nil {
			if attempt == maxAttempts {
				log.Printf("boot: initial reconcile failed after %d attempts: %v", maxAttempts, err)
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryInterval):
			}
			continue
		}
		return
	}
}

func (r *reconciler) run(ctx context.Context) error {
	// Initial reconcile with bounded retry — certgen may not be ready
	// yet immediately after boot. Proceed to event subscription regardless
	// of outcome (don't deadlock the daemon).
	r.bootReconcile(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Subscribe to container events.
	f := filters.NewArgs()
	for _, evt := range []string{"start", "stop", "die"} {
		f.Add("event", evt)
	}
	f.Add("type", "container")
	msgCh, errCh := r.docker.Events(ctx, events.ListOptions{Filters: f})

	// Network connect/disconnect uses type=network.
	fNet := filters.NewArgs()
	fNet.Add("event", "connect")
	fNet.Add("event", "disconnect")
	fNet.Add("type", "network")
	netCh, netErr := r.docker.Events(ctx, events.ListOptions{Filters: fNet})

	debounce := newDebouncer(r.debounce)
	defer debounce.stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg := <-msgCh:
			name := msg.Actor.Attributes["name"]
			if name == "" {
				name = msg.Actor.ID
			}
			log.Printf("event: container %s action=%s", name, msg.Action)
			debounce.bump()
		case msg := <-netCh:
			name := msg.Actor.Attributes["container"]
			if name == "" {
				name = msg.Actor.ID
			}
			log.Printf("event: network %s container=%s", msg.Action, name)
			debounce.bump()
		case err := <-errCh:
			return fmt.Errorf("docker container events: %w", err)
		case err := <-netErr:
			return fmt.Errorf("docker network events: %w", err)
		case <-debounce.fire():
			if err := r.reconcile(ctx); err != nil {
				log.Printf("reconcile: %v", err)
			}
		}
	}
}

func (r *reconciler) reconcile(ctx context.Context) error {
	containers, err := r.listEligible(ctx)
	if err != nil {
		return err
	}
	return r.reconcileWith(ctx, containers)
}

// reconcileWith performs the project-set / cert / config-write pipeline
// against an already-collected container list. Split out from reconcile
// so unit tests can drive it without a Docker daemon.
//
// Invariant: when the project set changes we MUST call certgen.Reissue
// (and wait for it to succeed) before writing the new auto.yml. If we
// wrote first, Traefik would route a hostname the cert has no SAN for.
func (r *reconciler) reconcileWith(ctx context.Context, containers []container.InspectResponse) error {
	network := r.network
	if network == "" {
		network = inspect.DefaultNetworkName
	}

	// Determine project set; reissue cert if changed. The set is computed
	// from containers ON THE MESH NETWORK only — routes are already scoped
	// the same way (see inspect.FromContainer), and the two must agree.
	// Including every compose project on the host would leak unrelated
	// project names into the cert's SANs, and a single project whose name
	// isn't a valid hostname label (e.g. contains underscores) would 400
	// the entire reissue, breaking TLS for every mesh hostname.
	projects := r.meshProjects(containers, network)
	if !reflect.DeepEqual(projects, r.lastProjects) {
		req := certgen.ReissueRequest{
			Suffixes:      r.suffixes,
			MachineName:   r.machine,
			TailnetDomain: r.tailnet,
			Projects:      projects,
		}
		sanCount := len(req.Suffixes) * (len(req.Projects) + 1) // rough estimate
		first, last := sanSummary(req.Projects)
		log.Printf("reissue: projects=%d suffixes=%d ~sans=%d projects=[%s..%s]",
			len(req.Projects), len(req.Suffixes), sanCount, first, last)
		_, _, err := r.certgen.Reissue(ctx, req)
		if err != nil {
			log.Printf("reissue: error: %v", err)
			return fmt.Errorf("certgen reissue: %w", err)
		}
		log.Printf("reissue: ok")
		// Don't promote lastProjects yet — if render or writeOut
		// fails below, the next reconcile must re-enter this branch
		// so it retries. certgen.Reissue is idempotent on identical
		// SAN sets (it short-circuits), so no mkcert thrash.
	}

	// Build render input.
	cfg := render.Config{
		Suffixes:      r.suffixes,
		MachineName:   r.machine,
		TailnetDomain: r.tailnet,
	}
	for _, c := range containers {
		r.logMisconfigIfNeeded(c, network)
		host, addr, ok, err := inspect.FromContainer(c, network)
		if err != nil {
			id := c.ID
			if len(id) > 12 {
				id = id[:12]
			}
			log.Printf("inspect %s: %v", id, err)
			continue
		}
		if !ok {
			continue
		}
		// Skip hostnames that aren't valid labels rather than letting render
		// reject the whole batch (docs/hostname-rules.md: "skipped — no
		// router is generated, and a log line explains why"). render keeps
		// its own validation as defense-in-depth; this is the primary skip.
		if !certgen.ValidateLabel(host) {
			if r.loggedInvalidHostname == nil {
				r.loggedInvalidHostname = map[string]bool{}
			}
			if !r.loggedInvalidHostname[host] {
				log.Printf("skip: container hostname %q is not a valid label "+
					"(must match [a-z0-9][a-z0-9.-]*, no underscores); no router "+
					"generated. Rename the container/compose project to a valid label.", host)
				r.loggedInvalidHostname[host] = true
			}
			continue
		}
		cfg.Containers = append(cfg.Containers, render.Container{
			Hostname: host,
			Address:  addr,
		})
	}

	yaml, err := r.render(cfg)
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}
	if err := r.writeOut(r.out, yaml); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	r.lastProjects = projects
	return nil
}

// meshProjects returns the cert project set for containers attached to the
// given network: distinct, sorted, and restricted to names that are valid
// hostname labels. A project whose name is not a valid label (e.g. it
// contains underscores) is skipped with a one-time log line rather than
// failing the whole reissue — matching the documented "invalid project →
// skipped, logged" contract (docs/hostname-rules.md). Skipping degrades
// gracefully: that project's services go unserved, but every other
// project's cert still issues.
func (r *reconciler) meshProjects(containers []container.InspectResponse, network string) []string {
	mesh := make([]container.InspectResponse, 0, len(containers))
	for _, c := range containers {
		if inspect.OnNetwork(c, network) {
			mesh = append(mesh, c)
		}
	}
	all := inspect.ProjectsOf(mesh)
	out := all[:0]
	for _, p := range all {
		if certgen.ValidateLabel(p) {
			out = append(out, p)
			continue
		}
		if r.loggedInvalidProject == nil {
			r.loggedInvalidProject = map[string]bool{}
		}
		if !r.loggedInvalidProject[p] {
			log.Printf("skip: compose project %q is not a valid hostname label "+
				"(must match [a-z0-9][a-z0-9.-]*, no underscores); its services "+
				"will not receive a mesh cert SAN. Set COMPOSE_PROJECT_NAME to a "+
				"valid label to serve it.", p)
			r.loggedInvalidProject[p] = true
		}
	}
	return out
}

// logMisconfigIfNeeded emits a remediation warning the first time it
// sees a labeled container missing the target network, and again only
// when the container's attached-network set changes.
func (r *reconciler) logMisconfigIfNeeded(c container.InspectResponse, network string) {
	attached, bad := inspect.LabeledMissingNetwork(c, network)
	id := c.ID
	if !bad {
		// Recovered (or never was bad): forget any prior fingerprint
		// so a future regression re-logs.
		delete(r.loggedMisconfig, id)
		return
	}
	fingerprint := strings.Join(attached, ",")
	if r.loggedMisconfig[id] == fingerprint {
		return
	}
	// State mutation MUST come AFTER the side effect (log call).
	name := strings.TrimPrefix(c.Name, "/")
	project := ""
	if c.Config != nil {
		project = c.Config.Labels["com.docker.compose.project"]
	}
	log.Printf(
		"WARN container=%s project=%s has traefik.* labels but is not attached to %s. "+
			"Traefik's docker provider will silently ignore it. "+
			"Fix: add `%s` to the service's networks list, or set label "+
			"`traefik.docker.network=<your-network>`. Networks attached: %v",
		name, project, network, network, attached,
	)
	r.loggedMisconfig[id] = fingerprint
}

func (r *reconciler) listEligible(ctx context.Context) ([]container.InspectResponse, error) {
	list, err := r.docker.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]container.InspectResponse, 0, len(list))
	for _, c := range list {
		full, err := r.docker.ContainerInspect(ctx, c.ID)
		if err != nil {
			id := c.ID
			if len(id) > 12 {
				id = id[:12]
			}
			log.Printf("inspect %s: %v", id, err)
			continue
		}
		out = append(out, full)
	}
	return out, nil
}

// sanSummary returns first and last project names for log brevity.
// Both are "<none>" when the slice is empty.
func sanSummary(projects []string) (first, last string) {
	if len(projects) == 0 {
		return "<none>", "<none>"
	}
	return projects[0], projects[len(projects)-1]
}

func atomicWrite(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	// Sync before rename so a kernel crash between write and rename
	// can't leave Traefik reading a torn or zero-byte file.
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write([]byte(content)); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
