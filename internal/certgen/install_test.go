package certgen

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRecordingMkcert installs a fake mkcert that appends each
// invocation's args + CAROOT to a log file, so the test can assert
// `-install` was called exactly once.
func writeRecordingMkcert(t *testing.T, dir, logPath string) string {
	t.Helper()
	script := `#!/bin/sh
echo "ARGS=$*  CAROOT=$CAROOT" >> "` + logPath + `"
`
	path := filepath.Join(dir, "mkcert")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInstallCAInvokesMkcertInstall(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	bin := writeRecordingMkcert(t, dir, logPath)
	caRoot := filepath.Join(dir, "caroot")

	if err := InstallCA(context.Background(), bin, caRoot); err != nil {
		t.Fatalf("InstallCA: %v", err)
	}
	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(got)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected mkcert invoked exactly once, got %d lines: %s", len(lines), got)
	}
	if !strings.Contains(lines[0], "ARGS=-install") {
		t.Errorf("expected -install arg, got %q", lines[0])
	}
	if !strings.Contains(lines[0], "CAROOT="+caRoot) {
		t.Errorf("expected CAROOT=%s, got %q", caRoot, lines[0])
	}
}

func TestInstallCAFailurePropagates(t *testing.T) {
	if err := InstallCA(context.Background(), "/nonexistent/mkcert-binary", ""); err == nil {
		t.Error("expected error for missing mkcert binary")
	}
}
