package subtitles

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	defaultGuangYaPanAPIBaseURL = "https://api.guangyapan.com"
	guangYaPanSubtitlePath      = "/misc/v1/get_subtitles"
	maxGuangYaPanResponseBytes  = 4 << 20
	defaultGuangYaPanTimeout    = 30 * time.Second
)

// GuangYaPanConfig exposes only transport settings. The anonymous subtitle
// client has no account, token, client ID, or device identity to configure.
// BaseURL exists for tests; production leaves it empty and uses the official
// GuangYaPan API origin.
type GuangYaPanConfig struct {
	BaseURL    string
	HTTPClient *http.Client
}

// GuangYaPanClient queries GuangYaPan's public subtitle endpoint anonymously.
type GuangYaPanClient struct {
	endpoint string
	client   *http.Client
}

func NewGuangYaPanClient(cfg GuangYaPanConfig) *GuangYaPanClient {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultGuangYaPanAPIBaseURL
	}
	return &GuangYaPanClient{
		endpoint: baseURL + guangYaPanSubtitlePath,
		client:   anonymousHTTPClient(cfg.HTTPClient),
	}
}

func anonymousHTTPClient(source *http.Client) *http.Client {
	if source == nil {
		return &http.Client{
			Timeout: defaultGuangYaPanTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	client := *source
	// A caller-provided cookie jar must never turn this anonymous client into an
	// account-bearing request. Redirects are also rejected so headers or cookies
	// cannot be introduced by a redirected origin.
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	if client.Timeout <= 0 {
		client.Timeout = defaultGuangYaPanTimeout
	}
	return &client
}

func (c *GuangYaPanClient) Subtitles(ctx context.Context, req Request) ([]Subtitle, error) {
	if c == nil || c.client == nil || strings.TrimSpace(c.endpoint) == "" {
		return nil, errors.New("guangyapan anonymous subtitles: client is not configured")
	}
	lookupKey, err := subtitleLookupKey(req)
	if err != nil {
		return nil, err
	}
	duration := req.DurationSeconds
	if duration < 0 {
		duration = 0
	}
	names := subtitleLookupNames(req)
	for _, name := range names {
		subs, err := c.query(ctx, lookupKey, name, duration)
		if err != nil {
			return nil, err
		}
		subs = filterDurationCompatibleSubtitles(subs, duration)
		if len(subs) > 0 {
			return subs, nil
		}
	}

	// The alias-only, duration-zero retry preserves the previous fuzzy lookup
	// fallback. Results whose known duration is clearly incompatible are dropped.
	if len(req.LookupNames) == 0 || duration == 0 {
		return []Subtitle{}, nil
	}
	fallbackName := ""
	for _, candidate := range req.LookupNames {
		if candidate = strings.TrimSpace(candidate); candidate != "" {
			fallbackName = candidate
		}
	}
	if fallbackName == "" {
		return []Subtitle{}, nil
	}
	subs, err := c.query(ctx, lookupKey, fallbackName, 0)
	if err != nil {
		return nil, err
	}
	return filterDurationCompatibleSubtitles(subs, duration), nil
}

func (c *GuangYaPanClient) query(ctx context.Context, lookupKey, name string, duration int) ([]Subtitle, error) {
	body, err := json.Marshal(map[string]any{
		"gcid":     lookupKey,
		"name":     name,
		"duration": duration,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	// These are the only explicit request headers. In particular, do not add
	// Authorization, Cookie, Did, device, client, or account headers.
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("guangyapan anonymous subtitles: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxGuangYaPanResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("guangyapan anonymous subtitles: read response: %w", err)
	}
	if len(data) > maxGuangYaPanResponseBytes {
		return nil, errors.New("guangyapan anonymous subtitles: response is too large")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("guangyapan anonymous subtitles: status=%d", resp.StatusCode)
	}

	var envelope guangYaPanSubtitleResponse
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("guangyapan anonymous subtitles: decode response: %w", err)
	}
	if envelope.Code != 0 {
		return nil, fmt.Errorf("guangyapan anonymous subtitles: code=%d msg=%s", envelope.Code, strings.TrimSpace(envelope.Msg))
	}
	out := make([]Subtitle, 0, len(envelope.Data.List))
	for _, item := range envelope.Data.List {
		if sub, ok := guangYaPanItemToSubtitle(item); ok {
			out = append(out, sub)
		}
	}
	return out, nil
}

func subtitleLookupKey(req Request) (string, error) {
	for _, candidate := range []string{req.ContentHash, req.SampledSHA256} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if gcid := normalizeGCID(candidate); gcid != "" {
			return gcid, nil
		}
		return candidate, nil
	}
	seed := strings.TrimSpace(req.FileID)
	if seed == "" {
		seed = strings.TrimSpace(req.FileName)
	}
	if seed == "" {
		return "", errors.New("guangyapan anonymous subtitles: lookup metadata is empty")
	}
	digest := sha1.Sum([]byte(seed))
	return strings.ToUpper(hex.EncodeToString(digest[:])), nil
}

func subtitleLookupNames(req Request) []string {
	// LookupNames are semantic aliases supplied by the catalog. At present they
	// are extracted AV codes, which are more reliable than noisy release names.
	candidates := make([]string, 0, len(req.LookupNames)+1)
	candidates = append(candidates, req.LookupNames...)
	candidates = append(candidates, req.FileName)
	out := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		key := strings.ToLower(candidate)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, candidate)
	}
	if len(out) == 0 {
		if fallback := strings.TrimSpace(req.FileID); fallback != "" {
			out = append(out, fallback)
		}
	}
	return out
}

func filterDurationCompatibleSubtitles(subs []Subtitle, videoDuration int) []Subtitle {
	out := make([]Subtitle, 0, len(subs))
	for _, sub := range subs {
		if DurationCompatible(videoDuration, sub.DurationSeconds) {
			out = append(out, sub)
		}
	}
	return out
}

type guangYaPanSubtitleResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		List []guangYaPanSubtitleItem `json:"list"`
	} `json:"data"`
}

type guangYaPanSubtitleItem struct {
	GCID      string   `json:"gcid"`
	CID       string   `json:"cid"`
	Source    int      `json:"source"`
	Name      string   `json:"name"`
	Ext       string   `json:"ext"`
	Duration  int64    `json:"duration"`
	Languages []string `json:"languages"`
	URL       string   `json:"url"`
}

func guangYaPanItemToSubtitle(item guangYaPanSubtitleItem) (Subtitle, bool) {
	rawURL := strings.TrimSpace(item.URL)
	if rawURL == "" {
		return Subtitle{}, false
	}
	ext := normalizeSubtitleExt(item.Ext)
	if ext == "" {
		ext = normalizeSubtitleExt(path.Ext(parsedPath(rawURL)))
	}
	id := strings.TrimSpace(item.GCID)
	if id == "" {
		id = strings.TrimSpace(item.CID)
	}
	sourceLabel := "online"
	if item.Source == 1 {
		sourceLabel = "inner"
	}
	duration := int(item.Duration)
	if duration > 24*60*60 {
		duration /= 1000
	}
	return Subtitle{
		ID:              id,
		Name:            strings.TrimSpace(item.Name),
		Ext:             ext,
		Language:        firstLanguage(item.Languages),
		URL:             rawURL,
		Source:          item.Source,
		SourceLabel:     sourceLabel,
		DurationSeconds: duration,
	}, true
}

func parsedPath(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u == nil {
		return rawURL
	}
	return u.Path
}

func firstLanguage(values []string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func normalizeSubtitleExt(value string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(value), "."))
}

func normalizeGCID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) != 40 {
		return ""
	}
	for _, ch := range value {
		switch {
		case ch >= '0' && ch <= '9':
		case ch >= 'a' && ch <= 'f':
		case ch >= 'A' && ch <= 'F':
		default:
			return ""
		}
	}
	return strings.ToUpper(value)
}

var _ Client = (*GuangYaPanClient)(nil)
