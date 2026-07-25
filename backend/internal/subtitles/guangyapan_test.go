package subtitles

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestGuangYaPanClientRequestsFixedEndpointAnonymously(t *testing.T) {
	const gcid = "0123456789abcdef0123456789abcdef01234567"
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != guangYaPanSubtitlePath {
			t.Fatalf("request = %s %s, want POST %s", r.Method, r.URL.Path, guangYaPanSubtitlePath)
		}
		for name, values := range r.Header {
			lower := strings.ToLower(name)
			if lower == "authorization" || lower == "cookie" || lower == "did" || lower == "dt" ||
				strings.Contains(lower, "device") || strings.Contains(lower, "account") || strings.Contains(lower, "client-id") {
				t.Fatalf("anonymous request leaked header %s=%q", name, values)
			}
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if len(body) != 3 || body["gcid"] != strings.ToUpper(gcid) || body["name"] != "HND-970.mp4" || body["duration"] != float64(257) {
			t.Fatalf("request body = %#v", body)
		}
		writeSubtitleJSON(t, w, map[string]any{
			"code": 0,
			"msg":  "success",
			"data": map[string]any{"list": []map[string]any{
				{
					"gcid":      gcid,
					"source":    1,
					"name":      "简体中文",
					"duration":  257000,
					"languages": []string{"zh-CN"},
					"url":       "https://sub.example/HND-970.srt?token=signed",
				},
				{
					"cid":       "online-2",
					"source":    2,
					"name":      "English",
					"ext":       ".vtt",
					"duration":  257,
					"languages": []string{"", "en"},
					"url":       "https://sub.example/HND-970.vtt",
				},
				{"cid": "empty-url", "ext": "srt"},
			}},
		})
	}))
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	serverURL, _ := url.Parse(server.URL)
	jar.SetCookies(serverURL, []*http.Cookie{{Name: "account_session", Value: "must-not-leak"}})
	client := NewGuangYaPanClient(GuangYaPanConfig{
		BaseURL:    server.URL,
		HTTPClient: &http.Client{Jar: jar},
	})
	subs, err := client.Subtitles(context.Background(), Request{
		FileID:          "file-1",
		FileName:        "HND-970.mp4",
		ContentHash:     gcid,
		DurationSeconds: 257,
	})
	if err != nil {
		t.Fatalf("Subtitles: %v", err)
	}
	if calls != 1 || len(subs) != 2 {
		t.Fatalf("calls=%d subtitles=%#v, want one call and two usable results", calls, subs)
	}
	if subs[0].ID != gcid || subs[0].Ext != "srt" || subs[0].Language != "zh-CN" || subs[0].SourceLabel != "inner" || subs[0].DurationSeconds != 257 {
		t.Fatalf("first subtitle = %#v", subs[0])
	}
	if subs[1].ID != "online-2" || subs[1].Ext != "vtt" || subs[1].Language != "en" || subs[1].SourceLabel != "online" {
		t.Fatalf("second subtitle = %#v", subs[1])
	}
}

func TestSubtitleLookupKeyPriority(t *testing.T) {
	fileIDDigest := sha1.Sum([]byte("file-1"))
	fileNameDigest := sha1.Sum([]byte("movie.mp4"))
	tests := []struct {
		name    string
		req     Request
		want    string
		wantErr bool
	}{
		{
			name: "real gcid content hash is normalized and preferred",
			req: Request{
				ContentHash:   " 0123456789abcdef0123456789abcdef01234567 ",
				SampledSHA256: "sampled",
				FileID:        "file-1",
			},
			want: "0123456789ABCDEF0123456789ABCDEF01234567",
		},
		{name: "other content hash is preserved", req: Request{ContentHash: " md5-value ", SampledSHA256: "sampled"}, want: "md5-value"},
		{name: "sampled sha256 follows content hash", req: Request{SampledSHA256: " sampled-value ", FileID: "file-1"}, want: "sampled-value"},
		{name: "file id synthetic key", req: Request{FileID: "file-1", FileName: "movie.mp4"}, want: strings.ToUpper(hex.EncodeToString(fileIDDigest[:]))},
		{name: "file name synthetic key", req: Request{FileName: "movie.mp4"}, want: strings.ToUpper(hex.EncodeToString(fileNameDigest[:]))},
		{name: "empty metadata", req: Request{}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := subtitleLookupKey(tt.req)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("subtitleLookupKey() = %q, want error", got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("subtitleLookupKey() = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestGuangYaPanClientUsesPreferredAliasFilenameAndDurationFallback(t *testing.T) {
	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		requests = append(requests, body)
		list := []map[string]any{}
		if body["name"] == "HND-970" && body["duration"] == float64(0) {
			list = []map[string]any{
				{"cid": "unknown", "ext": "srt", "duration": 0, "url": "https://sub.example/unknown.srt"},
				{"cid": "close", "ext": "ass", "duration": 7220000, "url": "https://sub.example/close.ass"},
				{"cid": "wrong", "ext": "srt", "duration": 6800000, "url": "https://sub.example/wrong.srt"},
			}
		}
		writeSubtitleJSON(t, w, map[string]any{"code": 0, "data": map[string]any{"list": list}})
	}))
	defer server.Close()

	client := NewGuangYaPanClient(GuangYaPanConfig{BaseURL: server.URL})
	subs, err := client.Subtitles(context.Background(), Request{
		FileID:          "file-1",
		FileName:        "long original HND-970 title.mp4",
		LookupNames:     []string{"HND-970"},
		SampledSHA256:   "sampled-hash",
		DurationSeconds: 7256,
	})
	if err != nil {
		t.Fatalf("Subtitles: %v", err)
	}
	if len(requests) != 3 {
		t.Fatalf("requests = %#v, want alias, filename, then zero-duration alias", requests)
	}
	wantNames := []string{"HND-970", "long original HND-970 title.mp4", "HND-970"}
	wantDurations := []float64{7256, 7256, 0}
	for i := range requests {
		if requests[i]["name"] != wantNames[i] || requests[i]["duration"] != wantDurations[i] || requests[i]["gcid"] != "sampled-hash" {
			t.Fatalf("request %d = %#v", i, requests[i])
		}
	}
	if got := subtitleIDsForTest(subs); !reflect.DeepEqual(got, []string{"unknown", "close"}) {
		t.Fatalf("subtitle IDs = %#v, want duration-compatible fallback results", got)
	}
}

func TestGuangYaPanClientPrefersAVCodeAndDropsIncompatibleResults(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		name, _ := body["name"].(string)
		requests = append(requests, name)
		list := []map[string]any{}
		switch name {
		case "MIMK-284":
			list = []map[string]any{
				{"cid": "correct", "name": "MIMK-284.srt", "ext": "srt", "duration": 9295000, "url": "https://sub.example/MIMK-284.srt"},
				{"cid": "wrong-duration", "name": "DLDSS-493.srt", "ext": "srt", "duration": 8695000, "url": "https://sub.example/DLDSS-493.srt"},
			}
		case "www.example@MIMK-284.restored-S_2_prob4.mp4":
			list = []map[string]any{
				{"cid": "wrong-name", "name": "START-397.srt", "ext": "srt", "duration": 9336000, "url": "https://sub.example/START-397.srt"},
			}
		}
		writeSubtitleJSON(t, w, map[string]any{"code": 0, "data": map[string]any{"list": list}})
	}))
	defer server.Close()

	client := NewGuangYaPanClient(GuangYaPanConfig{BaseURL: server.URL})
	subs, err := client.Subtitles(context.Background(), Request{
		FileID:          "file-1",
		FileName:        "www.example@MIMK-284.restored-S_2_prob4.mp4",
		LookupNames:     []string{"MIMK-284"},
		SampledSHA256:   "sampled-hash",
		DurationSeconds: 9336,
	})
	if err != nil {
		t.Fatalf("Subtitles: %v", err)
	}
	if !reflect.DeepEqual(requests, []string{"MIMK-284"}) {
		t.Fatalf("lookup requests = %#v, want AV code only after a usable match", requests)
	}
	if got := subtitleIDsForTest(subs); !reflect.DeepEqual(got, []string{"correct"}) {
		t.Fatalf("subtitle IDs = %#v, want only duration-compatible AV-code result", got)
	}
}

func TestGuangYaPanClientReturnsUpstreamFailures(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "limited", status: http.StatusTooManyRequests, body: `{"code":429,"msg":"limited"}`},
		{name: "server error", status: http.StatusBadGateway, body: `{"code":0}`},
		{name: "invalid json", status: http.StatusOK, body: `{`},
		{name: "application error", status: http.StatusOK, body: `{"code":503,"msg":"busy"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			client := NewGuangYaPanClient(GuangYaPanConfig{BaseURL: server.URL})
			if _, err := client.Subtitles(context.Background(), Request{FileID: "file-1", FileName: "movie.mp4"}); err == nil {
				t.Fatal("Subtitles succeeded, want an upstream error")
			}
		})
	}
}

func subtitleIDsForTest(subs []Subtitle) []string {
	out := make([]string, len(subs))
	for index, sub := range subs {
		out[index] = sub.ID
	}
	return out
}

func writeSubtitleJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}
