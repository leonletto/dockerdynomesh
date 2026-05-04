package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/leonletto/dockerdynomesh/internal/certgen"
)

func main() {
	var (
		certPath   = flag.String("cert", "/shared/certs/wildcard.crt", "wildcard cert path")
		keyPath    = flag.String("key", "/shared/certs/wildcard.key", "wildcard key path")
		socketPath = flag.String("socket", "/shared/run/certgen.sock", "Unix socket path")
		mkcertBin  = flag.String("mkcert", "mkcert", "mkcert binary path")
		caRoot     = flag.String("caroot", "/root/.local/share/mkcert", "mkcert CAROOT directory (volume-mounted for persistence)")
	)
	flag.Parse()

	if err := os.MkdirAll(*caRoot, 0o755); err != nil {
		log.Fatalf("mkdir caroot: %v", err)
	}
	if err := certgen.InstallCA(context.Background(), *mkcertBin, *caRoot); err != nil {
		log.Fatalf("install CA: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(*socketPath), 0o755); err != nil {
		log.Fatalf("mkdir socket dir: %v", err)
	}
	_ = os.Remove(*socketPath)

	listener, err := net.Listen("unix", *socketPath)
	if err != nil {
		log.Fatalf("listen unix %s: %v", *socketPath, err)
	}
	defer listener.Close()
	// 0o666: discoverer now runs as nonroot (UID 65532, dockerdynomesh-ggu)
	// and needs to connect to this socket. The socket lives in a named
	// Docker volume (not host-mounted), so world-readable is safe here.
	if err := os.Chmod(*socketPath, 0o666); err != nil {
		log.Fatalf("chmod socket: %v", err)
	}

	srv := &certgen.Server{R: &certgen.Reissuer{
		CertPath:  *certPath,
		KeyPath:   *keyPath,
		MkcertBin: *mkcertBin,
		CARootDir: *caRoot,
	}}
	httpServer := &http.Server{Handler: srv.Routes()}
	log.Printf("certgen listening on %s", *socketPath)

	serveErr := make(chan error, 1)
	go func() {
		err := httpServer.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
		close(serveErr)
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	select {
	case s := <-sig:
		log.Printf("received %s, shutting down", s)
	case err := <-serveErr:
		log.Fatalf("serve: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
