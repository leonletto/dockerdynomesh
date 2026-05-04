// Package welcome serves the root cert and install instructions.
// All handlers run on plain HTTP because the transport is a Tailscale
// tunnel (authenticated, encrypted between tailnet peers) and the
// install flow verifies the downloaded root against the live TLS
// endpoint before trusting it.
package welcome

import (
	"embed"
	"html/template"
	"log"
	"net/http"
	"os"
	textTemplate "text/template"
)

//go:embed templates/*.tmpl
var fs embed.FS

type Server struct {
	MachineName  string
	Suffix       string
	TailnetHost  string // e.g. host-mbp.tail0123.ts.net (used for TLS verification)
	SetupHost    string // e.g. setup.host-mbp.tail0123.ts.net (HTTP-only, this server)
	RootCertPath string

	indexTmpl   *template.Template
	installTmpl *textTemplate.Template
}

func New(machineName, suffix, tailnetHost, setupHost, rootCertPath string) (*Server, error) {
	idx, err := template.ParseFS(fs, "templates/index.html.tmpl")
	if err != nil {
		return nil, err
	}
	ins, err := textTemplate.ParseFS(fs, "templates/install.sh.tmpl")
	if err != nil {
		return nil, err
	}
	return &Server{
		MachineName:  machineName,
		Suffix:       suffix,
		TailnetHost:  tailnetHost,
		SetupHost:    setupHost,
		RootCertPath: rootCertPath,
		indexTmpl:    idx,
		installTmpl:  ins,
	}, nil
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/root.crt", s.handleRootCert)
	mux.HandleFunc("/install.sh", s.handleInstall)
	return mux
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.indexTmpl.Execute(w, s); err != nil {
		log.Printf("welcome: index template execute: %v", err)
	}
}

func (s *Server) handleRootCert(w http.ResponseWriter, r *http.Request) {
	b, err := os.ReadFile(s.RootCertPath)
	if err != nil {
		http.Error(w, "root cert not yet available", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="root.crt"`)
	_, _ = w.Write(b)
}

func (s *Server) handleInstall(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	if err := s.installTmpl.Execute(w, s); err != nil {
		log.Printf("welcome: install template execute: %v", err)
	}
}
