package certgen

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fakePEM = `-----BEGIN CERTIFICATE-----
MIIDKDCCAhCgAwIBAgIUaxsEqB8RotbAFKD5Z2JB5j1/xeAwDQYJKoZIhvcNAQEL
BQAwFDESMBAGA1UEAwwJZmFrZS10ZXN0MB4XDTI2MDUwNDAxNDQzNloXDTQ2MDQy
OTAxNDQzNlowFDESMBAGA1UEAwwJZmFrZS10ZXN0MIIBIjANBgkqhkiG9w0BAQEF
AAOCAQ8AMIIBCgKCAQEAoURD8mZm+hAnrtiVylPP4YNlrktEkyLwCVax6DWmalpU
wYh6irxAqrYOZbsACcbT7MJB+J1mw7WDwP4EDvoyl1woa0oBaubJoikIJvGYGh23
tHrWBOL26a7pZSuBDqlH4KKaW+eIbhTIXqJLf8bvxY3cfcDdjRXZ5aCS1xiaKfWF
JXoQ3FXQAHxI6/+4bZ0hCuBV1evOg4Paze0FjCdPH4xtCRe8RT7sGPt5kraLN8tH
zoG4vO4qrG7IAf2P+GVpTNQC5EOjPS5JKr16WWL1cSJhevbMgx/mHIaEo4ECSqUd
Untm5OKlkC2ElGNF4yhAy7vbT6cYXg/gkGxGX8ELfQIDAQABo3IwcDAdBgNVHQ4E
FgQUmDhXkXAyRxcyEdQhWeaHZEXAD4UwHwYDVR0jBBgwFoAUmDhXkXAyRxcyEdQh
WeaHZEXAD4UwDwYDVR0TAQH/BAUwAwEB/zAdBgNVHREEFjAUghIqLmRvY2tlci5s
b2NhbGhvc3QwDQYJKoZIhvcNAQELBQADggEBAGpQm9/lCFecAaz4tfVdHbzygiE+
NuL4BaYjtiGpRDGyjJzgGRNdRhfWTTxydDR+p5S9H5dSBwgutezdX/zLNQkgrcLu
6SImWSH9qL+hwPQJFuAtyDMRR8ud2Ogdd5ixgdD1e/PPnbC/i8jZzV4htPyluXXd
OL7SyjcaxWOAR9VBaT3s2lRyk0M8cMoMAg4v78scicON/BK7aiNeB48iKG9dzhb1
a12IdTW1zS/Fc8onVACvWdfvEEwHNfceLV4fzZ0CKCqCRf8BtMtQL+4ybQi8I+mq
gX9xy3tk+bkZp0P7YzGpRh5sgKgUjJthBEClPacQftG7rRKKy8bujDnJOHY=
-----END CERTIFICATE-----
`

const fakeKey = `-----BEGIN PRIVATE KEY-----
MIIEuwIBADANBgkqhkiG9w0BAQEFAASCBKUwggShAgEAAoIBAQChREPyZmb6ECeu
2JXKU8/hg2WuS0STIvAJVrHoNaZqWlTBiHqKvECqtg5luwAJxtPswkH4nWbDtYPA
/gQO+jKXXChrSgFq5smiKQgm8ZgaHbe0etYE4vbprullK4EOqUfgoppb54huFMhe
okt/xu/Fjdx9wN2NFdnloJLXGJop9YUlehDcVdAAfEjr/7htnSEK4FXV686Dg9rN
7QWMJ08fjG0JF7xFPuwY+3mStos3y0fOgbi87iqsbsgB/Y/4ZWlM1ALkQ6M9Lkkq
vXpZYvVxImF69syDH+YchoSjgQJKpR1Se2bk4qWQLYSUY0XjKEDLu9tPpxheD+CQ
bEZfwQt9AgMBAAECgf8TwlIIacfe7NeJezNFn5jhd6b1NBB/smpQVDVnE5IlqPzK
l5BsKvoFrFnkC/z9vjQoGvpxyJMbmtUa6gS3aqwumzWt758atcXtuqHWI7LMCOks
gK1A7AWqJLu4XNfzQLl0O4klqIWO1TBARfe16/bbRkq1P+PDTAOzfM6BFOB5ZZSO
XA7RuXJt6VLrys3S56zNHj6QPyKAA30waqMiVAguki2T3+pLRBgCYZDOuXR4KP2i
+b3gHgGJQe+nWqCa64RliDThWgUBr2EzuQH86vZpepQ14Q/mtuL1DyiTzpyMdnoE
EdneHF9KQH4VFOR9PomPqNZXooW7BmEzv8TvTRECgYEA1kBR1dQW/hzZfxZz0ldN
rp9OxjKru6GlYH1VM5QRr6lYgMzLJDjYyEigsmb1AC/o5drbCiDWGCe0T3xW2wTo
PbIjFYMv3/yWz5IHkHmci1Q9ufKdRggLTYFNjnaoYny6qsH2D8sUZpZGna4Komx+
mk64MkAGpngNEhUdlrOI+lkCgYEAwLDeqahbHeWbjXIdyR5mCVuS0isTvnAHX+dD
iozn7F6Cma3bwEurEYQc9GyVxu67iFZojy36Bqgez8Igd80D7OWlv01JW6t4TRXV
qdrw/IE24zL+4lJ26AEj4o+cDCtwXLPH/ZAv2/FFci6mpmwGM+kLSl9LCXDqPPxL
7Um07cUCgYEAljH67JLFF5kz48LiqQco3wyxFYJ6H4wfOjhCnWjkySdHcuueUSNE
3YsElGxWvq3XcCNvwHbqf35+CebZoKqdAHs72x3fVv9k3di6Us7eLlJ8/zkUhf6n
pcrKit+mBXz5AzH8BHBSOeSJVoqmy9yRGC2tNRTrVJH+X7nLx1TO5ukCgYBZ+n2P
RdF+jXhsvWwRPUOyfPN7dqgall+rNefBK/kk1CEyOBBUpED2xfVrYcUzBsnFaWwb
6AFH2HvC0kitCKwblEUopqNpzhE4FckXLui3UHNb9rU05AMoZVfndN4OhL5MW5s4
2XqvvuOJ5STms6zV0q32BbeZagPHhJzD6lY1bQKBgFlpGqoaKhWAOoZ8yAsJ+ZHt
pVgQpukr6ghr3xzY/F5FbOmzrOi0XDqpeI06DZe7r1HoS0b78QjsldBAM1w3Wa7z
dOR6JQJUz4y/BZfnicwWxoieKDXw4DkjfhX7o7d1Q4CL4mD214VXSEBOb3StLBnf
jPTwkTrLXUpQ8EMVPvP3
-----END PRIVATE KEY-----
`

// writeFakeMkcert installs a tiny shell script on disk that mimics
// mkcert's CLI: it accepts -cert-file/-key-file flags and writes a
// known-valid (self-signed) PEM pair to those paths. Real mkcert
// invocation is covered by integration tests, not here.
func writeFakeMkcert(t *testing.T, dir string) string {
	t.Helper()
	script := `#!/bin/sh
while [ $# -gt 0 ]; do
  case "$1" in
    -cert-file) shift; CRT="$1" ;;
    -key-file)  shift; KEY="$1" ;;
  esac
  shift
done
cat > "$CRT" <<'EOF'
` + fakePEM + `EOF
cat > "$KEY" <<'EOF'
` + fakeKey + `EOF
`
	path := filepath.Join(dir, "mkcert")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReissueWritesFiles(t *testing.T) {
	dir := t.TempDir()
	mkcert := writeFakeMkcert(t, dir)
	r := &Reissuer{
		CertPath:  filepath.Join(dir, "wildcard.crt"),
		KeyPath:   filepath.Join(dir, "wildcard.key"),
		MkcertBin: mkcert,
	}
	ok, err := r.Reissue(context.Background(), []string{"*.docker.localhost"})
	if err != nil {
		t.Fatalf("Reissue: %v", err)
	}
	if !ok {
		t.Error("expected reissued=true on first run")
	}
	if _, err := os.Stat(r.CertPath); err != nil {
		t.Errorf("cert not written: %v", err)
	}
	if _, err := os.Stat(r.KeyPath); err != nil {
		t.Errorf("key not written: %v", err)
	}
}

// TestReissuePartialRenameFailure verifies the documented contract for
// rename-sequence failures: rename key first then cert, so a mid-sequence
// failure cannot leave a silent new-cert+old-key mismatch. The visible
// outcome on second-rename failure is new-key+old-cert which a TLS
// handshake will reject loudly; the next Reissue call re-converges.
func TestReissuePartialRenameFailure(t *testing.T) {
	dir := t.TempDir()
	mkcert := writeFakeMkcert(t, dir)
	calls := 0
	r := &Reissuer{
		CertPath:  filepath.Join(dir, "wildcard.crt"),
		KeyPath:   filepath.Join(dir, "wildcard.key"),
		MkcertBin: mkcert,
		Rename: func(oldpath, newpath string) error {
			calls++
			if calls == 2 {
				return errSimulated
			}
			return os.Rename(oldpath, newpath)
		},
	}
	_, err := r.Reissue(context.Background(), []string{"*.docker.localhost"})
	if err == nil {
		t.Fatal("expected rename error to propagate")
	}
	// Key rename succeeded (call 1); cert rename failed (call 2).
	if _, statErr := os.Stat(r.KeyPath); statErr != nil {
		t.Errorf("expected key file to exist (rename 1 succeeded): %v", statErr)
	}
	if _, statErr := os.Stat(r.CertPath); !os.IsNotExist(statErr) {
		t.Errorf("expected cert file NOT to exist (rename 2 failed): %v", statErr)
	}
	// .tmp files are cleaned up by deferred Remove.
	if _, statErr := os.Stat(r.CertPath + ".tmp"); !os.IsNotExist(statErr) {
		t.Errorf("expected cert .tmp cleaned up: %v", statErr)
	}
}

var errSimulated = fmt.Errorf("simulated rename failure")

// writeFakeMkcertEnvDump installs a tiny shell script that writes its env to
// a file before behaving like the normal fake mkcert. Used to assert that
// cmd.Env is set correctly.
func writeFakeMkcertEnvDump(t *testing.T, dir, envFile string) string {
	t.Helper()
	script := `#!/bin/sh
env > ` + envFile + `
while [ $# -gt 0 ]; do
  case "$1" in
    -cert-file) shift; CRT="$1" ;;
    -key-file)  shift; KEY="$1" ;;
  esac
  shift
done
cat > "$CRT" <<'EOF'
` + fakePEM + `EOF
cat > "$KEY" <<'EOF'
` + fakeKey + `EOF
`
	path := filepath.Join(dir, "mkcert-env")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReissueSetsCARootEnv(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "env.txt")
	mkcert := writeFakeMkcertEnvDump(t, dir, envFile)
	caRoot := "/fake/caroot"
	r := &Reissuer{
		CertPath:  filepath.Join(dir, "wildcard.crt"),
		KeyPath:   filepath.Join(dir, "wildcard.key"),
		MkcertBin: mkcert,
		CARootDir: caRoot,
	}
	if _, err := r.Reissue(context.Background(), []string{"*.docker.localhost"}); err != nil {
		t.Fatalf("Reissue: %v", err)
	}
	envBytes, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	envStr := string(envBytes)
	wantLine := "CAROOT=" + caRoot
	found := false
	for _, line := range strings.Split(envStr, "\n") {
		if line == wantLine {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("CAROOT not set in cmd.Env; env dump:\n%s", envStr)
	}
}

func TestReissueNoCARootEnvWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "env.txt")
	mkcert := writeFakeMkcertEnvDump(t, dir, envFile)
	r := &Reissuer{
		CertPath:  filepath.Join(dir, "wildcard.crt"),
		KeyPath:   filepath.Join(dir, "wildcard.key"),
		MkcertBin: mkcert,
		// CARootDir intentionally empty — back-compat.
	}
	if _, err := r.Reissue(context.Background(), []string{"*.docker.localhost"}); err != nil {
		t.Fatalf("Reissue: %v", err)
	}
	envBytes, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	envStr := string(envBytes)
	for _, line := range strings.Split(envStr, "\n") {
		if strings.HasPrefix(line, "CAROOT=") {
			t.Errorf("unexpected CAROOT in env when CARootDir is empty: %s", line)
		}
	}
	// Verify parent env is present (PATH must always be set).
	hasPath := false
	for _, line := range strings.Split(envStr, "\n") {
		if strings.HasPrefix(line, "PATH=") {
			hasPath = true
			break
		}
	}
	if !hasPath {
		t.Error("PATH not found in env dump; parent env not forwarded")
	}
}

func TestReissueIdempotent(t *testing.T) {
	dir := t.TempDir()
	mkcert := writeFakeMkcert(t, dir)
	r := &Reissuer{
		CertPath:  filepath.Join(dir, "wildcard.crt"),
		KeyPath:   filepath.Join(dir, "wildcard.key"),
		MkcertBin: mkcert,
	}
	sans := []string{"*.docker.localhost"}
	if _, err := r.Reissue(context.Background(), sans); err != nil {
		t.Fatal(err)
	}
	ok, err := r.Reissue(context.Background(), sans)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("expected reissued=false on second identical call")
	}
}
