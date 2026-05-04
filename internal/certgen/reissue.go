package certgen

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"time"
)

// Reissuer maintains certs at certPath and keyPath. It runs mkcert
// with the configured CAROOT (mkcert's own env var) — typically a
// volume-mounted dir so the root CA is durable across container
// restarts. Reissue is no-op when the requested SANs match the
// current cert's SAN list AND the cert is more than 30 days from
// expiry.
type Reissuer struct {
	CertPath  string // e.g. /shared/certs/wildcard.crt
	KeyPath   string // e.g. /shared/certs/wildcard.key
	MkcertBin string // optional override; default "mkcert"
	CARootDir string // mkcert CAROOT env var; empty → mkcert default
	// Rename is the function used to commit the .tmp files into place.
	// Defaults to os.Rename. Tests inject a fault-injecting variant to
	// exercise the partial-failure recovery contract.
	Rename func(oldpath, newpath string) error
}

// Reissue generates a new cert covering sans if needed.
// Returns reissued=true when a new cert was written, false on no-op.
func (r *Reissuer) Reissue(ctx context.Context, sans []string) (reissued bool, err error) {
	cur, curErr := r.currentSANs()
	if curErr == nil && sansEqual(cur.SANs, sans) && time.Until(cur.NotAfter) > 30*24*time.Hour {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(r.CertPath), 0o755); err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(r.KeyPath), 0o755); err != nil {
		return false, err
	}
	bin := r.MkcertBin
	if bin == "" {
		bin = "mkcert"
	}
	tmpCert := r.CertPath + ".tmp"
	tmpKey := r.KeyPath + ".tmp"
	// Best-effort cleanup of any temp files left behind on partial failure.
	// os.Rename consumes the source on success, so this is a no-op then.
	defer os.Remove(tmpCert)
	defer os.Remove(tmpKey)

	args := append([]string{"-cert-file", tmpCert, "-key-file", tmpKey}, sans...)
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = os.Environ()
	if r.CARootDir != "" {
		cmd.Env = append(cmd.Env, "CAROOT="+r.CARootDir)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("mkcert failed: %w", err)
	}
	rename := r.Rename
	if rename == nil {
		rename = os.Rename
	}
	// Rename key BEFORE cert so a partial failure leaves either the old
	// pair (key rename failed) or the new pair (key rename succeeded,
	// cert rename failed → next reissue retries cert). A reversed order
	// could leave new-cert + old-key on disk, which is silent breakage;
	// the chosen order yields a key/cert mismatch detectable on the
	// first TLS handshake.
	if err := rename(tmpKey, r.KeyPath); err != nil {
		return false, err
	}
	if err := rename(tmpCert, r.CertPath); err != nil {
		return false, err
	}
	return true, nil
}

type certInfo struct {
	SANs     []string
	NotAfter time.Time
}

func (r *Reissuer) currentSANs() (certInfo, error) {
	b, err := os.ReadFile(r.CertPath)
	if err != nil {
		return certInfo{}, err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return certInfo{}, fmt.Errorf("no PEM in %s", r.CertPath)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return certInfo{}, err
	}
	sans := append([]string(nil), cert.DNSNames...)
	sort.Strings(sans)
	return certInfo{SANs: sans, NotAfter: cert.NotAfter}, nil
}

func sansEqual(a, b []string) bool {
	// Both inputs are already sorted (currentSANs and SANs guarantee it),
	// so a direct elementwise compare is sufficient.
	return slices.Equal(a, b)
}
