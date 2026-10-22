package utils

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsPublicIP(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{ip: "127.0.0.1", want: true},
		{ip: "10.0.0.1", want: false},
		{ip: "172.16.0.1", want: false},
		{ip: "192.168.1.1", want: false},
		{ip: "169.254.169.254", want: false},
		{ip: "100.64.0.1", want: false},
		{ip: "::1", want: true},
		{ip: "8.8.8.8", want: true},
		{ip: "2001:4860:4860::8888", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			if got := isPublicIP(net.ParseIP(tt.ip)); got != tt.want {
				t.Fatalf("isPublicIP(%s) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestNewCallbackClientAllowsLoopback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	client := NewCallbackClient()
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("callback client should allow loopback: %v", err)
	}
	_ = resp.Body.Close()
}

func TestSafeDialContextBlocksPrivateIP(t *testing.T) {
	if _, err := safeDialContext(context.Background(), "tcp", "10.0.0.1:80"); err == nil {
		t.Fatal("private address should be blocked")
	}
}

func TestAppendQuery(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		params string
		want   string
	}{
		{name: "no query", raw: "https://example.com/notify", params: "a=1", want: "https://example.com/notify?a=1"},
		{name: "existing query", raw: "https://example.com/notify?b=2", params: "a=1", want: "https://example.com/notify?b=2&a=1"},
		{name: "trailing question", raw: "https://example.com/notify?", params: "a=1", want: "https://example.com/notify?a=1"},
		{name: "fragment", raw: "https://example.com/notify#result", params: "a=1", want: "https://example.com/notify?a=1#result"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AppendQuery(tt.raw, tt.params); got != tt.want {
				t.Fatalf("AppendQuery(%q, %q) = %q, want %q", tt.raw, tt.params, got, tt.want)
			}
		})
	}
}

func TestIsAllowedCallbackURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{name: "https", url: "https://example.com/notify?order_id=1", want: true},
		{name: "http", url: "http://127.0.0.1:8080/notify", want: true},
		{name: "empty", url: "", want: false},
		{name: "javascript", url: "javascript:alert(1)", want: false},
		{name: "ftp", url: "ftp://example.com/notify", want: false},
		{name: "html injection", url: `https://example.com/?x="><img src=x onerror=alert(1)>`, want: false},
		{name: "apostrophe", url: "https://example.com/a'b", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAllowedCallbackURL(tt.url); got != tt.want {
				t.Fatalf("IsAllowedCallbackURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}
