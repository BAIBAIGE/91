// Package scopedproxy routes HTTP requests through an explicitly configured
// proxy only when their context opts in. It lets a shared drive client serve
// ordinary playback/list operations directly while one crawler upload uses a
// crawler-specific proxy.
package scopedproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	xproxy "golang.org/x/net/proxy"
)

type contextKey struct{}

var (
	ErrInvalidURL        = errors.New("invalid proxy URL")
	ErrUnsupportedScheme = errors.New("unsupported proxy scheme")
)

type config struct {
	key string
	url *url.URL
}

// Normalize validates the proxy schemes supported by the scoped transport.
// The returned URL remains suitable for persistence, including optional
// userinfo used for proxy authentication.
func Normalize(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || strings.TrimSpace(u.Scheme) == "" || strings.TrimSpace(u.Host) == "" {
		return "", ErrInvalidURL
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5", "socks5h":
		return raw, nil
	default:
		return "", fmt.Errorf("%w %q", ErrUnsupportedScheme, u.Scheme)
	}
}

// WithURL marks child HTTP requests to use raw. An empty value deliberately
// leaves the context unchanged, preserving the direct/default transport path.
func WithURL(ctx context.Context, raw string) (context.Context, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	normalized, err := Normalize(raw)
	if err != nil {
		return nil, err
	}
	if normalized == "" {
		return ctx, nil
	}
	u, err := url.Parse(normalized)
	if err != nil {
		return nil, err
	}
	u.Scheme = strings.ToLower(u.Scheme)
	return context.WithValue(ctx, contextKey{}, &config{key: normalized, url: u}), nil
}

// Configured reports whether ctx has opted into a scoped proxy. It is useful
// for observability and tests without exposing proxy credentials.
func Configured(ctx context.Context) bool {
	return configFromContext(ctx) != nil
}

func configFromContext(ctx context.Context) *config {
	if ctx == nil {
		return nil
	}
	cfg, _ := ctx.Value(contextKey{}).(*config)
	return cfg
}

// Transport delegates ordinary requests to direct. Requests carrying a proxy
// config use a separate connection pool for that proxy, keyed by the complete
// proxy URL so credentials and endpoints can never share a tunnel.
type Transport struct {
	direct http.RoundTripper
	base   *http.Transport

	mu      sync.Mutex
	byProxy map[string]*http.Transport
}

// NewTransport wraps direct with context-scoped proxy selection. A nil direct
// transport means http.DefaultTransport, including its normal environment
// proxy behavior for requests that have not explicitly opted in.
func NewTransport(direct http.RoundTripper) *Transport {
	if direct == nil {
		direct = http.DefaultTransport
	}
	base := http.DefaultTransport.(*http.Transport).Clone()
	if transport, ok := direct.(*http.Transport); ok {
		base = transport.Clone()
	}
	return &Transport{
		direct:  direct,
		base:    base,
		byProxy: make(map[string]*http.Transport),
	}
}

// NewHTTPClient returns an HTTP client ready to honor scoped proxy contexts.
func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: NewTransport(nil)}
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, errors.New("scoped proxy: nil request")
	}
	cfg := configFromContext(req.Context())
	if cfg == nil {
		return t.direct.RoundTrip(req)
	}
	transport, err := t.transportFor(cfg)
	if err != nil {
		return nil, err
	}
	return transport.RoundTrip(req)
}

func (t *Transport) transportFor(cfg *config) (*http.Transport, error) {
	t.mu.Lock()
	if transport := t.byProxy[cfg.key]; transport != nil {
		t.mu.Unlock()
		return transport, nil
	}
	t.mu.Unlock()

	transport := t.base.Clone()
	switch cfg.url.Scheme {
	case "http", "https":
		transport.Proxy = http.ProxyURL(cfg.url)
	case "socks5", "socks5h":
		dialContext, err := socksDialContext(cfg.url)
		if err != nil {
			return nil, fmt.Errorf("scoped proxy: %w", err)
		}
		transport.Proxy = nil
		transport.DialContext = dialContext
	default:
		return nil, fmt.Errorf("scoped proxy: unsupported scheme %q", cfg.url.Scheme)
	}

	t.mu.Lock()
	if existing := t.byProxy[cfg.key]; existing != nil {
		t.mu.Unlock()
		transport.CloseIdleConnections()
		return existing, nil
	}
	t.byProxy[cfg.key] = transport
	t.mu.Unlock()
	return transport, nil
}

func socksDialContext(proxyURL *url.URL) (func(context.Context, string, string) (net.Conn, error), error) {
	var auth *xproxy.Auth
	if proxyURL.User != nil {
		password, _ := proxyURL.User.Password()
		auth = &xproxy.Auth{User: proxyURL.User.Username(), Password: password}
	}
	dialer, err := xproxy.SOCKS5("tcp", proxyURL.Host, auth, &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	remoteDNS := proxyURL.Scheme == "socks5h"
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		target := address
		if !remoteDNS {
			resolved, err := resolveSOCKSTarget(ctx, address)
			if err != nil {
				return nil, err
			}
			target = resolved
		}
		if contextDialer, ok := dialer.(xproxy.ContextDialer); ok {
			return contextDialer.DialContext(ctx, network, target)
		}

		type result struct {
			conn net.Conn
			err  error
		}
		resultCh := make(chan result, 1)
		go func() {
			conn, err := dialer.Dial(network, target)
			resultCh <- result{conn: conn, err: err}
		}()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case result := <-resultCh:
			return result.conn, result.err
		}
	}, nil
}

func resolveSOCKSTarget(ctx context.Context, address string) (string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) != nil {
		return address, err
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return "", err
	}
	if len(addresses) == 0 {
		return "", fmt.Errorf("no address found for %s", host)
	}
	return net.JoinHostPort(addresses[0].IP.String(), port), nil
}
