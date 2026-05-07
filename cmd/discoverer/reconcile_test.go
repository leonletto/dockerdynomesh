package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
	"github.com/leonletto/dockerdynomesh/internal/certgen"
	"github.com/leonletto/dockerdynomesh/internal/inspect"
	"github.com/leonletto/dockerdynomesh/internal/render"
)

type fakeReissuer struct {
	calls []certgen.ReissueRequest
	err   error
}

func (f *fakeReissuer) Reissue(_ context.Context, req certgen.ReissueRequest) (bool, []string, error) {
	f.calls = append(f.calls, req)
	if f.err != nil {
		return false, nil, f.err
	}
	return true, nil, nil
}

type writeCapture struct {
	calls []writeCall
}

type writeCall struct {
	path    string
	content string
}

func (w *writeCapture) write(path, content string) error {
	w.calls = append(w.calls, writeCall{path, content})
	return nil
}

// recordingReissuer records the relative order of Reissue and write
// calls so the test can assert reissue-before-publish.
type recordingReissuer struct {
	events *[]string
}

func (r *recordingReissuer) Reissue(_ context.Context, _ certgen.ReissueRequest) (bool, []string, error) {
	*r.events = append(*r.events, "reissue")
	return true, nil, nil
}

func makeContainer(name, project, service, ip string) container.InspectResponse {
	labels := map[string]string{}
	if project != "" {
		labels["com.docker.compose.project"] = project
	}
	if service != "" {
		labels["com.docker.compose.service"] = service
	}
	return container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{
			ID:   "abc123def4567890",
			Name: "/" + name,
		},
		Config: &container.Config{
			Labels:       labels,
			ExposedPorts: nat.PortSet{nat.Port("80/tcp"): {}},
		},
		NetworkSettings: &container.NetworkSettings{
			Networks: map[string]*network.EndpointSettings{
				inspect.DefaultNetworkName: {IPAddress: ip},
			},
		},
	}
}

func newReconciler(cg certReissuer, w func(string, string) error) *reconciler {
	return &reconciler{
		certgen:  cg,
		render:   render.Render,
		writeOut: w,
		out:      "/tmp/auto.yml",
		suffixes: []string{"docker.localhost"},
	}
}

func TestReconcileReissuesOnProjectSetChange(t *testing.T) {
	rs := &fakeReissuer{}
	wc := &writeCapture{}
	r := newReconciler(rs, wc.write)

	cs := []container.InspectResponse{
		makeContainer("repo-nginx-1", "repo", "nginx", "172.20.0.5"),
	}
	if err := r.reconcileWith(context.Background(), cs); err != nil {
		t.Fatal(err)
	}
	if len(rs.calls) != 1 {
		t.Fatalf("reissue calls = %d; want 1", len(rs.calls))
	}
	if got := rs.calls[0].Projects; len(got) != 1 || got[0] != "repo" {
		t.Errorf("projects = %v", got)
	}
	if len(wc.calls) != 1 {
		t.Fatalf("writes = %d; want 1", len(wc.calls))
	}
	if !strings.Contains(wc.calls[0].content, "nginx-repo") {
		t.Errorf("config missing router:\n%s", wc.calls[0].content)
	}
}

func TestReconcileSkipsReissueOnUnchangedProjects(t *testing.T) {
	rs := &fakeReissuer{}
	wc := &writeCapture{}
	r := newReconciler(rs, wc.write)

	cs := []container.InspectResponse{
		makeContainer("repo-nginx-1", "repo", "nginx", "172.20.0.5"),
	}
	if err := r.reconcileWith(context.Background(), cs); err != nil {
		t.Fatal(err)
	}
	if err := r.reconcileWith(context.Background(), cs); err != nil {
		t.Fatal(err)
	}
	if len(rs.calls) != 1 {
		t.Errorf("reissue calls = %d; want 1 (project set unchanged)", len(rs.calls))
	}
	// Both reconciles publish a config (rendering is cheap and the
	// listing may otherwise drift).
	if len(wc.calls) != 2 {
		t.Errorf("writes = %d; want 2", len(wc.calls))
	}
}

func TestReconcileReissueBeforePublish(t *testing.T) {
	var events []string
	rr := &recordingReissuer{events: &events}
	w := func(path, content string) error {
		events = append(events, "write")
		return nil
	}
	r := newReconciler(rr, w)
	cs := []container.InspectResponse{
		makeContainer("repo-nginx-1", "repo", "nginx", "172.20.0.5"),
	}
	if err := r.reconcileWith(context.Background(), cs); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0] != "reissue" || events[1] != "write" {
		t.Errorf("event order = %v; want [reissue write]", events)
	}
}

func TestReconcileDoesNotPublishWhenReissueFails(t *testing.T) {
	rs := &fakeReissuer{err: errors.New("boom")}
	wc := &writeCapture{}
	r := newReconciler(rs, wc.write)
	cs := []container.InspectResponse{
		makeContainer("repo-nginx-1", "repo", "nginx", "172.20.0.5"),
	}
	err := r.reconcileWith(context.Background(), cs)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "certgen reissue") {
		t.Errorf("error = %v", err)
	}
	if len(wc.calls) != 0 {
		t.Errorf("writes = %d; want 0 (reissue failed)", len(wc.calls))
	}
}

func TestReconcileReissuesAgainOnProjectAdded(t *testing.T) {
	rs := &fakeReissuer{}
	wc := &writeCapture{}
	r := newReconciler(rs, wc.write)

	cs1 := []container.InspectResponse{
		makeContainer("repo-nginx-1", "repo", "nginx", "172.20.0.5"),
	}
	cs2 := []container.InspectResponse{
		makeContainer("repo-nginx-1", "repo", "nginx", "172.20.0.5"),
		makeContainer("api-web-1", "api", "web", "172.20.0.6"),
	}
	if err := r.reconcileWith(context.Background(), cs1); err != nil {
		t.Fatal(err)
	}
	if err := r.reconcileWith(context.Background(), cs2); err != nil {
		t.Fatal(err)
	}
	if len(rs.calls) != 2 {
		t.Fatalf("reissue calls = %d; want 2", len(rs.calls))
	}
	if got := rs.calls[1].Projects; len(got) != 2 || got[0] != "api" || got[1] != "repo" {
		t.Errorf("second reissue projects = %v; want [api repo]", got)
	}
}

func TestReconcileLastProjectsNotUpdatedOnWriteFailure(t *testing.T) {
	rs := &fakeReissuer{}
	writeErr := errors.New("disk full")
	failingWrite := func(string, string) error { return writeErr }
	r := newReconciler(rs, failingWrite)

	cs := []container.InspectResponse{
		makeContainer("repo-nginx-1", "repo", "nginx", "172.20.0.5"),
	}
	if err := r.reconcileWith(context.Background(), cs); err == nil {
		t.Fatal("expected write error")
	}
	if r.lastProjects != nil {
		t.Errorf("lastProjects = %v; want nil after write failure", r.lastProjects)
	}

	// Second attempt with the same containers MUST re-enter the
	// reissue branch — proves no silent inconsistency on retry.
	wc := &writeCapture{}
	r.writeOut = wc.write
	if err := r.reconcileWith(context.Background(), cs); err != nil {
		t.Fatal(err)
	}
	if len(rs.calls) != 2 {
		t.Errorf("reissue calls = %d; want 2 (retry must re-enter)", len(rs.calls))
	}
	if len(r.lastProjects) != 1 || r.lastProjects[0] != "repo" {
		t.Errorf("lastProjects = %v; want [repo] after successful write", r.lastProjects)
	}
}

func TestReconcileSkipsContainerWithNilConfig(t *testing.T) {
	// Defensive: a partial-state container can have nil Config. Both
	// inspect.FromContainer and inspect.ProjectsOf must skip cleanly.
	rs := &fakeReissuer{}
	wc := &writeCapture{}
	r := newReconciler(rs, wc.write)

	cs := []container.InspectResponse{
		{ContainerJSONBase: &container.ContainerJSONBase{ID: "deadbeefcafe"}}, // Config nil
		makeContainer("repo-nginx-1", "repo", "nginx", "172.20.0.5"),
	}
	if err := r.reconcileWith(context.Background(), cs); err != nil {
		t.Fatalf("reconcile panicked or errored: %v", err)
	}
}

// newBootReconciler builds a minimal reconciler wired for bootReconcile tests.
// reconcileFn is injected; bootMaxAttempts and bootRetryInterval are set to
// tiny values so the tests finish instantly.
func newBootReconciler(fn func(ctx context.Context) error, maxAttempts int) *reconciler {
	return &reconciler{
		reconcileFn:       fn,
		bootMaxAttempts:   maxAttempts,
		bootRetryInterval: time.Nanosecond, // no real sleeping in tests
	}
}

func TestBootReconcileRetriesUpToMax(t *testing.T) {
	const maxAttempts = 5
	boom := errors.New("certgen not ready")
	var calls atomic.Int32
	fn := func(_ context.Context) error {
		calls.Add(1)
		return boom
	}
	r := newBootReconciler(fn, maxAttempts)
	r.bootReconcile(context.Background())
	if got := int(calls.Load()); got != maxAttempts {
		t.Errorf("calls = %d; want %d", got, maxAttempts)
	}
}

func TestBootReconcileStopsOnSuccess(t *testing.T) {
	const maxAttempts = 10
	const succeedOnAttempt = 3
	boom := errors.New("not yet")
	var calls atomic.Int32
	fn := func(_ context.Context) error {
		n := int(calls.Add(1))
		if n < succeedOnAttempt {
			return boom
		}
		return nil
	}
	r := newBootReconciler(fn, maxAttempts)
	r.bootReconcile(context.Background())
	if got := int(calls.Load()); got != succeedOnAttempt {
		t.Errorf("calls = %d; want %d (should stop on first success)", got, succeedOnAttempt)
	}
}

func TestBootReconcileRespectsCtxCancellation(t *testing.T) {
	const maxAttempts = 100
	boom := errors.New("always fails")
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	fn := func(_ context.Context) error {
		if calls.Add(1) >= 3 {
			cancel()
		}
		return boom
	}
	r := &reconciler{
		reconcileFn:       fn,
		bootMaxAttempts:   maxAttempts,
		bootRetryInterval: time.Nanosecond,
	}
	r.bootReconcile(ctx)
	// Should have stopped well before maxAttempts.
	if got := int(calls.Load()); got >= maxAttempts {
		t.Errorf("calls = %d; ctx cancellation did not break loop", got)
	}
}

func TestReconciler_LogsLabeledMisconfigurationOnce(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	r := &reconciler{
		loggedMisconfig: map[string]string{},
	}
	c := container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{ID: "deadbeefcafe", Name: "/falcon-demo-nginx"},
		Config: &container.Config{Labels: map[string]string{
			"traefik.enable":             "true",
			"com.docker.compose.project": "demo.falconmode",
		}},
		NetworkSettings: &container.NetworkSettings{Networks: map[string]*network.EndpointSettings{
			"app-net": {IPAddress: "10.0.0.2"},
		}},
	}

	r.logMisconfigIfNeeded(c, "dynomesh-net")
	r.logMisconfigIfNeeded(c, "dynomesh-net") // second call: must NOT re-log

	out := buf.String()
	if strings.Count(out, "not attached to dynomesh-net") != 1 {
		t.Fatalf("expected exactly one warning; got:\n%s", out)
	}
	for _, want := range []string{"falcon-demo-nginx", "demo.falconmode", "app-net", "traefik.docker.network"} {
		if !strings.Contains(out, want) {
			t.Fatalf("warning missing %q\n%s", want, out)
		}
	}
}
