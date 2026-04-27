package httpclient

import (
	"context"
	"crypto/tls"
	"log"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// TransportConfig defines the rules for bypassing censorship
// to reach the underlying APIs.
type TransportConfig struct {
	// TargetIP is one or more IP:port targets to dial instead of DNS.
	// Comma-separated for multiple, e.g. "216.239.38.120:443,142.250.110.95:443"
	// Single value still works: "216.239.38.120:443"
	TargetIP string

	// SNI is the Server Name Indication to use during the TLS handshake.
	// E.g., "google.com"
	SNI string

	// HostHeader is the Host header to inject in HTTP requests.
	// E.g., "www.googleapis.com"
	HostHeader string

	// InsecureSkipVerify allows bypassing certificate validation if necessary.
	InsecureSkipVerify bool
}

// parseTargets splits TargetIP on commas and returns trimmed, non-empty entries.
func parseTargets(raw string) []string {
	var out []string
	for _, s := range strings.Split(raw, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// hostRewriteTransport is an http.RoundTripper that rewrites the Host header.
type hostRewriteTransport struct {
	Transport  http.RoundTripper
	HostHeader string
}

func (t *hostRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.HostHeader != "" {
		req.Host = t.HostHeader
	}
	return t.Transport.RoundTrip(req)
}

// NewCustomClient creates an http.Client configured to bypass DNS
// and manipulate TLS/HTTP headers as specified in the config.
func NewCustomClient(cfg TransportConfig) *http.Client {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	targets := parseTargets(cfg.TargetIP)

	// Shuffle targets so different clients spread load
	rand.Shuffle(len(targets), func(i, j int) { targets[i], targets[j] = targets[j], targets[i] })

	var idx atomic.Int64

	transport := &http.Transport{
		// Dial with round-robin fallback across all target IPs.
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if len(targets) == 0 {
				return dialer.DialContext(ctx, network, addr)
			}

			// Try each target starting from the current index
			start := int(idx.Add(1)-1) % len(targets)
			var lastErr error
			for i := 0; i < len(targets); i++ {
				target := targets[(start+i)%len(targets)]
				conn, err := dialer.DialContext(ctx, "tcp", target)
				if err == nil {
					return conn, nil
				}
				lastErr = err
				log.Printf("Dial %s failed: %v, trying next...", target, err)
			}
			return nil, lastErr
		},
		TLSClientConfig: &tls.Config{
			ServerName:         cfg.SNI,
			InsecureSkipVerify: cfg.InsecureSkipVerify,
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	var rt http.RoundTripper = transport
	if cfg.HostHeader != "" {
		rt = &hostRewriteTransport{
			Transport:  transport,
			HostHeader: cfg.HostHeader,
		}
	}

	return &http.Client{
		Transport: rt,
		Timeout:   60 * time.Second,
	}
}
