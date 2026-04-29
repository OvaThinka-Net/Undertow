package httpclient

import (
	"net/http"
	"testing"
)

func TestParseTargets_Single(t *testing.T) {
	targets := parseTargets("1.2.3.4:443")
	if len(targets) != 1 || targets[0] != "1.2.3.4:443" {
		t.Errorf("got %v", targets)
	}
}

func TestParseTargets_Multiple(t *testing.T) {
	targets := parseTargets("1.2.3.4:443,5.6.7.8:443,9.10.11.12:443")
	if len(targets) != 3 {
		t.Fatalf("expected 3 targets, got %d: %v", len(targets), targets)
	}
	expected := []string{"1.2.3.4:443", "5.6.7.8:443", "9.10.11.12:443"}
	for i, want := range expected {
		if targets[i] != want {
			t.Errorf("target[%d]: got %q, want %q", i, targets[i], want)
		}
	}
}

func TestParseTargets_Whitespace(t *testing.T) {
	targets := parseTargets("  1.2.3.4:443 , 5.6.7.8:443  ")
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d: %v", len(targets), targets)
	}
	if targets[0] != "1.2.3.4:443" || targets[1] != "5.6.7.8:443" {
		t.Errorf("unexpected: %v", targets)
	}
}

func TestParseTargets_Empty(t *testing.T) {
	targets := parseTargets("")
	if len(targets) != 0 {
		t.Errorf("expected 0 targets for empty string, got %d", len(targets))
	}
}

func TestParseTargets_TrailingComma(t *testing.T) {
	targets := parseTargets("1.2.3.4:443,")
	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d: %v", len(targets), targets)
	}
}

func TestHostRewriteTransport(t *testing.T) {
	var capturedHost string
	inner := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		capturedHost = req.Host
		return &http.Response{StatusCode: 200}, nil
	})

	hrt := &hostRewriteTransport{
		Transport:  inner,
		HostHeader: "api.example.com",
	}

	req, _ := http.NewRequest("GET", "https://original.com/path", nil)
	hrt.RoundTrip(req)

	if capturedHost != "api.example.com" {
		t.Errorf("Host: got %q, want %q", capturedHost, "api.example.com")
	}
}

func TestHostRewriteTransport_EmptyHost(t *testing.T) {
	var capturedHost string
	inner := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		capturedHost = req.Host
		return &http.Response{StatusCode: 200}, nil
	})

	hrt := &hostRewriteTransport{
		Transport:  inner,
		HostHeader: "", // empty — should not rewrite
	}

	req, _ := http.NewRequest("GET", "https://original.com/path", nil)
	req.Host = "original.com"
	hrt.RoundTrip(req)

	if capturedHost != "original.com" {
		t.Errorf("Host should remain original: got %q", capturedHost)
	}
}

func TestNewCustomClient_NoTransport(t *testing.T) {
	// With empty config, should return a working client
	client := NewCustomClient(TransportConfig{})
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.Timeout == 0 {
		t.Error("expected non-zero timeout")
	}
}

func TestNewCustomClient_WithHostHeader(t *testing.T) {
	client := NewCustomClient(TransportConfig{
		HostHeader: "api.example.com",
	})
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	// The transport should be a hostRewriteTransport
	_, ok := client.Transport.(*hostRewriteTransport)
	if !ok {
		t.Error("expected hostRewriteTransport when HostHeader is set")
	}
}

func TestNewCustomClient_WithoutHostHeader(t *testing.T) {
	client := NewCustomClient(TransportConfig{
		TargetIP: "1.2.3.4:443",
		SNI:      "example.com",
	})
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	// Without HostHeader, transport should be *http.Transport directly
	_, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Errorf("expected *http.Transport, got %T", client.Transport)
	}
}

// roundTripFunc adapts a function to http.RoundTripper
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
