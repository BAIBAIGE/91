package scopedproxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTransportUsesProxyOnlyForOptedInContext(t *testing.T) {
	var originCalls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		originCalls.Add(1)
		_, _ = io.WriteString(w, "origin")
	}))
	t.Cleanup(origin.Close)

	var proxyCalls atomic.Int32
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalls.Add(1)
		if r.URL.Host == "" || !strings.HasSuffix(r.URL.Path, "/proxied") {
			t.Errorf("proxy request URL = %q, want absolute target ending /proxied", r.URL.String())
		}
		_, _ = io.WriteString(w, "proxy")
	}))
	t.Cleanup(proxyServer.Close)

	client := &http.Client{Transport: NewTransport(nil)}
	directResp, err := client.Get(origin.URL + "/direct")
	if err != nil {
		t.Fatalf("direct request: %v", err)
	}
	directBody, _ := io.ReadAll(directResp.Body)
	_ = directResp.Body.Close()
	if string(directBody) != "origin" || originCalls.Load() != 1 || proxyCalls.Load() != 0 {
		t.Fatalf("direct body/calls = %q/%d/%d", directBody, originCalls.Load(), proxyCalls.Load())
	}

	ctx, err := WithURL(context.Background(), proxyServer.URL)
	if err != nil {
		t.Fatalf("configure proxy: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin.URL+"/proxied", nil)
	if err != nil {
		t.Fatalf("new proxied request: %v", err)
	}
	proxiedResp, err := client.Do(req)
	if err != nil {
		t.Fatalf("proxied request: %v", err)
	}
	proxiedBody, _ := io.ReadAll(proxiedResp.Body)
	_ = proxiedResp.Body.Close()
	if string(proxiedBody) != "proxy" || originCalls.Load() != 1 || proxyCalls.Load() != 1 {
		t.Fatalf("proxied body/calls = %q/%d/%d", proxiedBody, originCalls.Load(), proxyCalls.Load())
	}

	afterResp, err := client.Get(origin.URL + "/direct-after-proxy")
	if err != nil {
		t.Fatalf("direct request after proxy: %v", err)
	}
	afterBody, _ := io.ReadAll(afterResp.Body)
	_ = afterResp.Body.Close()
	if string(afterBody) != "origin" || originCalls.Load() != 2 || proxyCalls.Load() != 1 {
		t.Fatalf("post-proxy direct body/calls = %q/%d/%d", afterBody, originCalls.Load(), proxyCalls.Load())
	}
}

func TestNormalizeSupportedSchemes(t *testing.T) {
	for _, value := range []string{
		"http://127.0.0.1:7890",
		"https://user:pass@proxy.example:443",
		"socks5://127.0.0.1:1080",
		"socks5h://proxy.example:1080",
	} {
		got, err := Normalize("  " + value + "  ")
		if err != nil || got != value {
			t.Fatalf("Normalize(%q) = %q, %v", value, got, err)
		}
	}
	for _, value := range []string{"proxy.example:7890", "ftp://proxy.example"} {
		if _, err := Normalize(value); err == nil {
			t.Fatalf("Normalize(%q) succeeded", value)
		}
	}
}
