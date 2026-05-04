package welcome

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "root.crt")
	if err := os.WriteFile(root, []byte("DUMMY-CERT-CONTENT"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New("host-mbp", "docker.localhost",
		"host-mbp.tail0123.ts.net",
		"setup.host-mbp.tail0123.ts.net",
		root)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func TestIndex(t *testing.T) {
	s, _ := newTestServer(t)
	rr := httptest.NewRecorder()
	s.Routes().ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "host-mbp") || !strings.Contains(body, "docker.localhost") {
		t.Errorf("body missing fields:\n%s", body)
	}
}

func TestRootCert(t *testing.T) {
	s, _ := newTestServer(t)
	rr := httptest.NewRecorder()
	s.Routes().ServeHTTP(rr, httptest.NewRequest("GET", "/root.crt", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if rr.Body.String() != "DUMMY-CERT-CONTENT" {
		t.Errorf("body = %q", rr.Body.String())
	}
	if !strings.Contains(rr.Header().Get("Content-Disposition"), "root.crt") {
		t.Errorf("missing attachment header")
	}
	if got := rr.Header().Get("Content-Type"); got != "application/x-x509-ca-cert" {
		t.Errorf("content-type = %q", got)
	}
}

func TestRootCertMissing(t *testing.T) {
	s, dir := newTestServer(t)
	if err := os.Remove(filepath.Join(dir, "root.crt")); err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	s.Routes().ServeHTTP(rr, httptest.NewRequest("GET", "/root.crt", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

func TestInstallScript(t *testing.T) {
	s, _ := newTestServer(t)
	rr := httptest.NewRecorder()
	s.Routes().ServeHTTP(rr, httptest.NewRequest("GET", "/install.sh", nil))
	body := rr.Body.String()
	if !strings.Contains(body, "verify_return_error") {
		t.Error("install script missing verify step")
	}
	if !strings.Contains(body, "exit 1") {
		t.Error("install script missing non-zero exit on verify failure")
	}
	if !strings.Contains(body, "host-mbp.tail0123.ts.net") {
		t.Error("install script missing TLS host")
	}
	if !strings.Contains(body, "setup.host-mbp.tail0123.ts.net") {
		t.Error("install script missing setup host (cert download URL)")
	}
}

func TestUnknownPath(t *testing.T) {
	s, _ := newTestServer(t)
	rr := httptest.NewRecorder()
	s.Routes().ServeHTTP(rr, httptest.NewRequest("GET", "/nope", nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}
