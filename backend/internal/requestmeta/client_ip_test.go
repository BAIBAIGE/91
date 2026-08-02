package requestmeta

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		forwarded  string
		realIP     string
		want       string
	}{
		{
			name:       "direct public peer",
			remoteAddr: "198.51.100.20:12345",
			want:       "198.51.100.20",
		},
		{
			name:       "trusted loopback proxy",
			remoteAddr: "127.0.0.1:12345",
			forwarded:  "203.0.113.12",
			want:       "203.0.113.12",
		},
		{
			name:       "rightmost forwarded address",
			remoteAddr: "127.0.0.1:12345",
			forwarded:  "198.51.100.99, 203.0.113.12",
			want:       "203.0.113.12",
		},
		{
			name:       "mapped IPv4 addresses",
			remoteAddr: "[::ffff:127.0.0.1]:12345",
			forwarded:  "::ffff:203.0.113.12",
			want:       "203.0.113.12",
		},
		{
			name:       "real IP fallback",
			remoteAddr: "[::1]:12345",
			forwarded:  "invalid",
			realIP:     "2001:db8::12",
			want:       "2001:db8::12",
		},
		{
			name:       "loopback without forwarding headers",
			remoteAddr: "[::1]:12345",
			want:       "::1",
		},
		{
			name:       "untrusted peer ignores spoofed headers",
			remoteAddr: "198.51.100.20:12345",
			forwarded:  "203.0.113.12",
			realIP:     "203.0.113.13",
			want:       "198.51.100.20",
		},
		{
			name:       "unbracketed IPv6 without port",
			remoteAddr: "2001:db8::20",
			want:       "2001:db8::20",
		},
		{
			name:       "invalid peer does not trust headers",
			remoteAddr: "not-an-address",
			forwarded:  "203.0.113.12",
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.forwarded != "" {
				req.Header.Set("X-Forwarded-For", tt.forwarded)
			}
			if tt.realIP != "" {
				req.Header.Set("X-Real-IP", tt.realIP)
			}
			if got := ClientIP(req); got != tt.want {
				t.Fatalf("ClientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClientIPHandlesNilRequest(t *testing.T) {
	if got := ClientIP(nil); got != "" {
		t.Fatalf("ClientIP(nil) = %q, want empty", got)
	}
}
