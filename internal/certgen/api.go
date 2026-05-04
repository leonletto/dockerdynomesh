package certgen

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// ReissueRequest is the body for POST /reissue.
type ReissueRequest struct {
	Suffixes      []string `json:"suffixes"`
	MachineName   string   `json:"machine_name,omitempty"`
	TailnetDomain string   `json:"tailnet_domain,omitempty"`
	Projects      []string `json:"projects"`
}

// ReissueResponse is returned on 200; on 204 (no-op) the body is empty.
type ReissueResponse struct {
	Reissued bool     `json:"reissued"`
	SANs     []string `json:"sans"`
}

// StatusResponse is returned by GET /status.
type StatusResponse struct {
	SANs     []string `json:"sans"`
	NotAfter string   `json:"not_after"` // RFC3339
}

// Server wraps a Reissuer with HTTP handlers and a mutex so concurrent
// reissue requests serialize.
type Server struct {
	R  *Reissuer
	mu sync.Mutex
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/reissue", s.handleReissue)
	mux.HandleFunc("/status", s.handleStatus)
	return mux
}

func (s *Server) handleReissue(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, 64*1024)
	var body ReissueRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(body.Suffixes) == 0 {
		http.Error(w, "suffixes is required", http.StatusBadRequest)
		return
	}
	for _, s := range body.Suffixes {
		if !ValidateLabel(s) {
			http.Error(w, "invalid suffix: "+s, http.StatusBadRequest)
			return
		}
	}
	if body.MachineName != "" && !ValidateLabel(body.MachineName) {
		http.Error(w, "invalid machine_name", http.StatusBadRequest)
		return
	}
	if body.TailnetDomain != "" && !ValidateLabel(body.TailnetDomain) {
		http.Error(w, "invalid tailnet_domain", http.StatusBadRequest)
		return
	}
	for _, p := range body.Projects {
		if !ValidateLabel(p) {
			http.Error(w, "invalid project: "+p, http.StatusBadRequest)
			return
		}
	}
	sans := SANs(body.Suffixes, body.MachineName, body.TailnetDomain, body.Projects)

	s.mu.Lock()
	defer s.mu.Unlock()

	ctx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
	defer cancel()
	reissued, err := s.R.Reissue(ctx, sans)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !reissued {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ReissueResponse{Reissued: true, SANs: sans})
}

func (s *Server) handleStatus(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Hold the same mutex as handleReissue so we can't observe a torn
	// state mid-rename.
	s.mu.Lock()
	defer s.mu.Unlock()
	info, err := s.R.currentSANs()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(StatusResponse{
		SANs:     info.SANs,
		NotAfter: info.NotAfter.Format(time.RFC3339),
	})
}
