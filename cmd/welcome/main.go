// Command welcome serves the verified-install bootstrap (HTML index,
// root.crt, install.sh template) over plain HTTP. It is reached
// through Traefik on the tailnet, before the user trusts our CA, so
// HTTPS would be useless here.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/leonletto/dockerdynomesh/internal/certgen"
	"github.com/leonletto/dockerdynomesh/internal/welcome"
)

// validateConfig charset-checks operator-supplied identifiers before
// they flow into the install.sh shell template. text/template performs
// no shell escaping, and the install script interpolates these into
// double-quoted bash strings where $(...) and backticks would still
// execute. Same charset rule as Epic A/B (certgen.ValidateLabel).
func validateConfig(machine, tailnet, suffix string) error {
	if machine != "" && !certgen.ValidateLabel(machine) {
		return fmt.Errorf("invalid -machine %q", machine)
	}
	if tailnet != "" && !certgen.ValidateLabel(tailnet) {
		return fmt.Errorf("invalid -tailnet %q", tailnet)
	}
	if !certgen.ValidateLabel(suffix) {
		return fmt.Errorf("invalid -suffix %q", suffix)
	}
	return nil
}

func main() {
	var (
		listenAddrs = flag.String("listen", "127.0.0.1:80", "comma-separated bind addrs")
		machine     = flag.String("machine", os.Getenv("MACHINE_NAME"), "machine short name")
		suffix      = flag.String("suffix", envOr("SUFFIX", "docker.localhost"), "hostname suffix")
		tailnet     = flag.String("tailnet", os.Getenv("TAILNET_DOMAIN"), "tailnet domain")
		rootCert    = flag.String("root", "/shared/ca/rootCA.pem", "root cert path")
	)
	flag.Parse()

	if err := validateConfig(*machine, *tailnet, *suffix); err != nil {
		log.Fatal(err)
	}

	tailnetHost := ""
	setupHost := ""
	if *machine != "" && *tailnet != "" {
		tailnetHost = *machine + "." + *tailnet
		setupHost = "setup." + tailnetHost
	}
	srv, err := welcome.New(*machine, *suffix, tailnetHost, setupHost, *rootCert)
	if err != nil {
		log.Fatal(err)
	}

	var listeners []net.Listener
	for _, addr := range strings.Split(*listenAddrs, ",") {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		l, err := net.Listen("tcp", addr)
		if err != nil {
			log.Fatalf("listen %s: %v", addr, err)
		}
		log.Printf("welcome listening on %s", addr)
		listeners = append(listeners, l)
	}
	if len(listeners) == 0 {
		log.Fatal("no listen addresses configured")
	}

	handler := srv.Routes()

	var (
		wg      sync.WaitGroup
		servers = make([]*http.Server, len(listeners))
	)
	for i, l := range listeners {
		hs := &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
		}
		servers[i] = hs
		wg.Add(1)
		go func(hs *http.Server, l net.Listener) {
			defer wg.Done()
			if err := hs.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("serve %s: %v", l.Addr(), err)
			}
		}(hs, l)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Print("shutdown signal received")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Shut down all servers in parallel: a sequential loop sharing
	// one deadline can starve later servers of their shutdown window.
	var sg sync.WaitGroup
	for _, hs := range servers {
		sg.Add(1)
		go func(hs *http.Server) {
			defer sg.Done()
			if err := hs.Shutdown(ctx); err != nil {
				log.Printf("shutdown: %v", err)
			}
		}(hs)
	}
	sg.Wait()
	wg.Wait()
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
