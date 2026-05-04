package certclient

import (
	"context"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/dockerdynomesh/internal/certgen"
)

func TestClientReissueOK(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "test.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/reissue", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"reissued":true,"sans":["*.docker.localhost"]}`))
	})
	go func() { _ = http.Serve(l, mux) }()

	c := New(sock)
	reissued, sans, err := c.Reissue(context.Background(), certgen.ReissueRequest{Suffixes: []string{"docker.localhost"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reissued {
		t.Error("expected reissued=true")
	}
	if len(sans) != 1 || sans[0] != "*.docker.localhost" {
		t.Errorf("sans = %v", sans)
	}
}

func TestClientReissueError(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "test.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/reissue", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	go func() { _ = http.Serve(l, mux) }()

	c := New(sock)
	_, _, err = c.Reissue(context.Background(), certgen.ReissueRequest{Suffixes: []string{"docker.localhost"}})
	if err == nil {
		t.Fatal("expected error on 500, got nil")
	}
}

func TestClientReissueNoOp(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "test.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/reissue", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	go func() { _ = http.Serve(l, mux) }()

	c := New(sock)
	reissued, _, err := c.Reissue(context.Background(), certgen.ReissueRequest{Suffixes: []string{"docker.localhost"}})
	if err != nil {
		t.Fatal(err)
	}
	if reissued {
		t.Error("expected reissued=false on 204")
	}
}

func TestReissueSendsSuffixesList(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "test.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	var got string
	mux := http.NewServeMux()
	mux.HandleFunc("/reissue", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		w.WriteHeader(http.StatusNoContent)
	})
	go func() { _ = http.Serve(l, mux) }()

	c := New(sock)
	_, _, err = c.Reissue(context.Background(), certgen.ReissueRequest{
		Suffixes: []string{"docker.localhost", "orb.local"},
		Projects: []string{"repo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"suffixes":["docker.localhost","orb.local"]`) {
		t.Fatalf("expected suffixes list in body, got: %s", got)
	}
}
