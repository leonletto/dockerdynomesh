// Package certclient is a thin HTTP client for the certgen Unix-socket API.
// Discoverer calls Reissue when the compose-project set changes and waits
// for the response before publishing new Traefik dynamic config.
package certclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/leonletto/dockerdynomesh/internal/certgen"
)

type Client struct {
	HTTP       *http.Client
	SocketPath string
}

func New(socketPath string) *Client {
	return &Client{
		SocketPath: socketPath,
		HTTP: &http.Client{
			Timeout: 60 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", socketPath)
				},
			},
		},
	}
}

// Reissue calls POST /reissue. Returns reissued=true on 200, false on 204
// (no-op). Any other status is an error.
func (c *Client) Reissue(ctx context.Context, req certgen.ReissueRequest) (reissued bool, sans []string, err error) {
	body, err := json.Marshal(req)
	if err != nil {
		return false, nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", "http://unix/reissue", bytes.NewReader(body))
	if err != nil {
		return false, nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return false, nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return false, nil, nil
	case http.StatusOK:
		var r certgen.ReissueResponse
		if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
			return false, nil, err
		}
		return r.Reissued, r.SANs, nil
	default:
		// Cap the error-body read so a misbehaving server can't blow up
		// the error string. 4 KiB is plenty for any sane error message.
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return false, nil, fmt.Errorf("certgen returned %d: %s", resp.StatusCode, b)
	}
}
