package remoteupload

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxRedirects = 5

type ipResolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

type urlPolicy struct {
	resolver ipResolver
	dialer   net.Dialer
}

func newURLPolicy() *urlPolicy {
	return &urlPolicy{
		resolver: net.DefaultResolver,
		dialer: net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		},
	}
}

func (p *urlPolicy) validate(ctx context.Context, rawURL string) (*url.URL, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, validationError("视频直链不能为空")
	}
	if len(rawURL) > 8192 {
		return nil, validationError("视频直链过长")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, validationError("视频直链格式无效")
	}
	if err := validateURLShape(u); err != nil {
		return nil, err
	}
	if _, err := p.publicIPs(ctx, u.Hostname()); err != nil {
		return nil, err
	}
	u.Fragment = ""
	return u, nil
}

func (p *urlPolicy) validateParsed(ctx context.Context, u *url.URL) error {
	if err := validateURLShape(u); err != nil {
		return err
	}
	_, err := p.publicIPs(ctx, u.Hostname())
	return err
}

func validateURLShape(u *url.URL) error {
	if u == nil || u.IsAbs() == false || u.Opaque != "" {
		return validationError("视频直链必须是完整的 HTTP/HTTPS URL")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return validationError("仅支持 HTTP/HTTPS 视频直链")
	}
	if u.User != nil {
		return validationError("视频直链不能包含用户名或密码")
	}
	if strings.TrimSpace(u.Hostname()) == "" {
		return validationError("视频直链缺少主机名")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return validationError("视频直链端口无效")
		}
	}
	if isHLSPath(u.EscapedPath()) || queryContainsHLS(u.Query()) {
		return validationError("不支持 HLS/m3u8 链接")
	}
	return nil
}

func isHLSPath(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.HasSuffix(value, ".m3u8") || strings.Contains(value, ".m3u8/")
}

func queryContainsHLS(values url.Values) bool {
	for _, items := range values {
		for _, item := range items {
			if isHLSPath(item) {
				return true
			}
		}
	}
	return false
}

func (p *urlPolicy) publicIPs(ctx context.Context, host string) ([]netip.Addr, error) {
	resolveCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if host == "" {
		return nil, validationError("视频直链缺少主机名")
	}
	var ips []netip.Addr
	if parsed, err := netip.ParseAddr(host); err == nil {
		ips = []netip.Addr{parsed.Unmap()}
	} else {
		resolved, err := p.resolver.LookupNetIP(resolveCtx, "ip", host)
		if err != nil || len(resolved) == 0 {
			return nil, validationError("无法解析视频直链主机")
		}
		ips = make([]netip.Addr, 0, len(resolved))
		for _, ip := range resolved {
			ips = append(ips, ip.Unmap())
		}
	}
	for _, ip := range ips {
		if !isPublicRemoteIP(ip) {
			return nil, validationError("视频直链必须指向公网地址")
		}
	}
	return ips, nil
}

func (p *urlPolicy) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("remote upload: invalid dial address")
	}
	ips, err := p.publicIPs(dialCtx, host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, ip := range ips {
		conn, err := p.dialer.DialContext(dialCtx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("no public address")
	}
	return nil, fmt.Errorf("remote upload: public address dial failed: %w", lastErr)
}

var blockedRemotePrefixes = mustPrefixes(
	"0.0.0.0/8",
	"10.0.0.0/8",
	"100.64.0.0/10",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"224.0.0.0/4",
	"240.0.0.0/4",
	"192.0.0.0/24",
	"192.0.2.0/24",
	"192.88.99.0/24",
	"198.18.0.0/15",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"::/96",
	"::1/128",
	"64:ff9b::/96",
	"64:ff9b:1::/48",
	"100::/64",
	"2001::/32",
	"2001:db8::/32",
	"2002::/16",
	"fc00::/7",
	"fe80::/10",
	"fec0::/10",
	"ff00::/8",
)

var cloudMetadataAddresses = map[netip.Addr]struct{}{
	netip.MustParseAddr("100.100.100.200"): {},
	netip.MustParseAddr("168.63.129.16"):   {},
	netip.MustParseAddr("169.254.169.254"): {},
	netip.MustParseAddr("169.254.170.2"):   {},
}

func mustPrefixes(values ...string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		out = append(out, netip.MustParsePrefix(value))
	}
	return out
}

func isPublicRemoteIP(ip netip.Addr) bool {
	if !ip.IsValid() {
		return false
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() ||
		ip.IsUnspecified() ||
		ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() {
		return false
	}
	if _, blocked := cloudMetadataAddresses[ip]; blocked {
		return false
	}
	for _, prefix := range blockedRemotePrefixes {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

func sourceLabel(u *url.URL) string {
	if u == nil {
		return ""
	}
	host := u.Host
	if host == "" {
		host = u.Hostname()
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	label := host + path
	const maxLabelRunes = 512
	runes := []rune(label)
	if len(runes) > maxLabelRunes {
		label = string(runes[:maxLabelRunes-1]) + "…"
	}
	return label
}
