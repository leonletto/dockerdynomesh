package certgen

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// InstallCA invokes `mkcert -install` with CAROOT set to caRoot. mkcert
// itself is idempotent — running -install when the root CA already exists
// in the trust store is a no-op — so this is safe to call on every
// process start. The CAROOT volume must be writable by the running uid.
//
// bin defaults to "mkcert" if empty; caRoot defaults to mkcert's own
// default (~/.local/share/mkcert) if empty.
func InstallCA(ctx context.Context, bin, caRoot string) error {
	if bin == "" {
		bin = "mkcert"
	}
	cmd := exec.CommandContext(ctx, bin, "-install")
	cmd.Env = os.Environ()
	if caRoot != "" {
		cmd.Env = append(cmd.Env, "CAROOT="+caRoot)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mkcert -install failed: %w: %s", err, out)
	}
	return nil
}
