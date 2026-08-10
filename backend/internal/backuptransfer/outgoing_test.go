package backuptransfer

import (
	"strings"
	"testing"
)

func TestNormalizeTargetURLSupportsHTTPAndHTTPS(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "plain HTTP IPv4 and port",
			raw:  "  http://192.0.2.10:9192/  ",
			want: "http://192.0.2.10:9192",
		},
		{
			name: "plain HTTP IPv6 and port",
			raw:  "http://[2001:db8::10]:9192",
			want: "http://[2001:db8::10]:9192",
		},
		{
			name: "HTTPS hostname",
			raw:  "https://backup.example.com/",
			want: "https://backup.example.com",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeTargetURL(test.raw)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("normalizeTargetURL(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}

func TestNormalizeTargetURLRejectsUnsafeOrUnsupportedURLs(t *testing.T) {
	tests := []struct {
		raw     string
		message string
	}{
		{raw: "192.0.2.10:9192", message: "完整的目标服务器"},
		{raw: "ftp://192.0.2.10:9192", message: "仅支持 HTTP 或 HTTPS"},
		{raw: "http://user:password@192.0.2.10:9192", message: "不能包含凭据"},
		{raw: "http://192.0.2.10:9192/admin", message: "不能包含额外路径"},
		{raw: "http://192.0.2.10:9192?token=value", message: "不能包含凭据、查询参数或片段"},
		{raw: "http://192.0.2.10:9192#fragment", message: "不能包含凭据、查询参数或片段"},
	}
	for _, test := range tests {
		t.Run(test.raw, func(t *testing.T) {
			if got, err := normalizeTargetURL(test.raw); err == nil {
				t.Fatalf("normalizeTargetURL(%q) = %q, want error", test.raw, got)
			} else if !strings.Contains(err.Error(), test.message) {
				t.Fatalf("normalizeTargetURL(%q) error = %q, want %q", test.raw, err, test.message)
			}
		})
	}
}
