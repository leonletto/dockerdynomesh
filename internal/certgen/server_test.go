package certgen

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestReissueHandlerSuccess(t *testing.T) {
	dir := t.TempDir()
	mkcert := writeFakeMkcert(t, dir)
	srv := &Server{R: &Reissuer{
		CertPath:  filepath.Join(dir, "wildcard.crt"),
		KeyPath:   filepath.Join(dir, "wildcard.key"),
		MkcertBin: mkcert,
	}}
	body, _ := json.Marshal(ReissueRequest{
		Suffixes: []string{"docker.localhost"},
		Projects: []string{"repo"},
	})
	req := httptest.NewRequest("POST", "/reissue", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rr.Code, rr.Body.String())
	}
	var resp ReissueResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Reissued || len(resp.SANs) == 0 {
		t.Errorf("response = %+v", resp)
	}
}

func TestReissueHandlerBadJSON(t *testing.T) {
	srv := &Server{R: &Reissuer{}}
	req := httptest.NewRequest("POST", "/reissue", bytes.NewReader([]byte("not json")))
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestReissueHandlerWrongMethod(t *testing.T) {
	srv := &Server{R: &Reissuer{}}
	req := httptest.NewRequest("GET", "/reissue", nil)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rr.Code)
	}
}

func TestReissueHandlerNoOp(t *testing.T) {
	dir := t.TempDir()
	mkcert := writeFakeMkcert(t, dir)
	srv := &Server{R: &Reissuer{
		CertPath:  filepath.Join(dir, "wildcard.crt"),
		KeyPath:   filepath.Join(dir, "wildcard.key"),
		MkcertBin: mkcert,
	}}
	// Empty projects: requested SANs = ["*.docker.localhost"] which matches
	// the fake mkcert's emitted cert (DNSNames=["*.docker.localhost"]) — so
	// the second call short-circuits to 204.
	body, _ := json.Marshal(ReissueRequest{Suffixes: []string{"docker.localhost"}})
	// First call seeds the cert.
	rr1 := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr1, httptest.NewRequest("POST", "/reissue", bytes.NewReader(body)))
	if rr1.Code != http.StatusOK {
		t.Fatalf("first call status = %d", rr1.Code)
	}
	// Second identical call must be a 204 no-op.
	rr2 := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr2, httptest.NewRequest("POST", "/reissue", bytes.NewReader(body)))
	if rr2.Code != http.StatusNoContent {
		t.Errorf("second call status = %d, want 204", rr2.Code)
	}
	if rr2.Body.Len() != 0 {
		t.Errorf("204 body should be empty, got %q", rr2.Body.String())
	}
}

func TestReissueHandlerOversizedBody(t *testing.T) {
	srv := &Server{R: &Reissuer{}}
	huge := bytes.Repeat([]byte("a"), 128*1024)
	body, _ := json.Marshal(ReissueRequest{Suffixes: []string{"docker.localhost"}, Projects: []string{string(huge)}})
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, httptest.NewRequest("POST", "/reissue", bytes.NewReader(body)))
	if rr.Code < 400 || rr.Code >= 500 {
		t.Errorf("status = %d, want 4xx", rr.Code)
	}
}

func TestReissueHandlerInvalidSuffix(t *testing.T) {
	srv := &Server{R: &Reissuer{}}
	for _, suffix := range []string{"", "bad suffix", "UPPER", "-leading"} {
		body, _ := json.Marshal(ReissueRequest{Suffixes: []string{suffix}})
		rr := httptest.NewRecorder()
		srv.Routes().ServeHTTP(rr, httptest.NewRequest("POST", "/reissue", bytes.NewReader(body)))
		if rr.Code != http.StatusBadRequest {
			t.Errorf("suffix %q: status = %d, want 400", suffix, rr.Code)
		}
	}
}

func TestReissueHandlerInvalidProject(t *testing.T) {
	srv := &Server{R: &Reissuer{}}
	body, _ := json.Marshal(ReissueRequest{
		Suffixes: []string{"docker.localhost"},
		Projects: []string{"bad name with spaces"},
	})
	req := httptest.NewRequest("POST", "/reissue", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestReissueHandlerInternalFailure(t *testing.T) {
	dir := t.TempDir()
	// Point MkcertBin at a path that doesn't exist; exec will fail.
	srv := &Server{R: &Reissuer{
		CertPath:  filepath.Join(dir, "wildcard.crt"),
		KeyPath:   filepath.Join(dir, "wildcard.key"),
		MkcertBin: filepath.Join(dir, "does-not-exist"),
	}}
	body, _ := json.Marshal(ReissueRequest{Suffixes: []string{"docker.localhost"}})
	req := httptest.NewRequest("POST", "/reissue", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}
}

func TestStatusHandler(t *testing.T) {
	dir := t.TempDir()
	mkcert := writeFakeMkcert(t, dir)
	srv := &Server{R: &Reissuer{
		CertPath:  filepath.Join(dir, "wildcard.crt"),
		KeyPath:   filepath.Join(dir, "wildcard.key"),
		MkcertBin: mkcert,
	}}
	// Seed the cert.
	body, _ := json.Marshal(ReissueRequest{Suffixes: []string{"docker.localhost"}})
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, httptest.NewRequest("POST", "/reissue", bytes.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("seed reissue failed: %d", rr.Code)
	}
	// GET /status.
	rr = httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, httptest.NewRequest("GET", "/status", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status code = %d body = %s", rr.Code, rr.Body.String())
	}
	var resp StatusResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.SANs) == 0 || resp.NotAfter == "" {
		t.Errorf("unexpected status response: %+v", resp)
	}
}

func TestStatusHandlerWrongMethod(t *testing.T) {
	srv := &Server{R: &Reissuer{}}
	req := httptest.NewRequest("POST", "/status", nil)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rr.Code)
	}
}

func TestReissueAcceptsMultipleSuffixes(t *testing.T) {
	dir := t.TempDir()
	mkcert := writeFakeMkcert(t, dir)
	srv := &Server{R: &Reissuer{
		CertPath:  filepath.Join(dir, "wildcard.crt"),
		KeyPath:   filepath.Join(dir, "wildcard.key"),
		MkcertBin: mkcert,
	}}
	body := `{"suffixes":["docker.localhost","orb.local"],"projects":["repo"]}`
	req := httptest.NewRequest(http.MethodPost, "/reissue", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
		t.Fatalf("expected 200 or 204, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReissueRejectsInvalidSuffixInList(t *testing.T) {
	srv := &Server{R: &Reissuer{}}
	body := `{"suffixes":["docker.localhost","BAD..NAME"],"projects":["repo"]}`
	req := httptest.NewRequest(http.MethodPost, "/reissue", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReissueRejectsEmptySuffixes(t *testing.T) {
	srv := &Server{R: &Reissuer{}}
	body := `{"suffixes":[],"projects":["repo"]}`
	req := httptest.NewRequest(http.MethodPost, "/reissue", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}
