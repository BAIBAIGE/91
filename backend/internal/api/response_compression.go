package api

import (
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/andybalholm/brotli"
)

const logResponseBrotliQuality = 4

type compressedResponseWriter struct {
	http.ResponseWriter
	writer io.Writer
}

func (w *compressedResponseWriter) Write(payload []byte) (int, error) {
	return w.writer.Write(payload)
}

func compressLogResponse(w http.ResponseWriter, r *http.Request) (http.ResponseWriter, func()) {
	addVaryHeader(w.Header(), "Accept-Encoding")

	switch negotiateContentEncoding(r.Header.Values("Accept-Encoding")) {
	case "br":
		writer := brotli.NewWriterLevel(w, logResponseBrotliQuality)
		prepareCompressedResponseHeaders(w.Header(), "br")
		return &compressedResponseWriter{ResponseWriter: w, writer: writer}, func() {
			_ = writer.Close()
		}
	case "gzip":
		writer, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
		if err != nil {
			return w, func() {}
		}
		prepareCompressedResponseHeaders(w.Header(), "gzip")
		return &compressedResponseWriter{ResponseWriter: w, writer: writer}, func() {
			_ = writer.Close()
		}
	default:
		return w, func() {}
	}
}

func prepareCompressedResponseHeaders(header http.Header, encoding string) {
	header.Set("Content-Encoding", encoding)
	header.Del("Content-Length")
}

func negotiateContentEncoding(values []string) string {
	if len(values) == 0 {
		return ""
	}

	qualities := make(map[string]float64)
	specified := make(map[string]bool)
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			parts := strings.Split(item, ";")
			name := strings.ToLower(strings.TrimSpace(parts[0]))
			if name == "" {
				continue
			}
			quality := 1.0
			for _, parameter := range parts[1:] {
				key, raw, found := strings.Cut(strings.TrimSpace(parameter), "=")
				if !found || !strings.EqualFold(strings.TrimSpace(key), "q") {
					continue
				}
				parsed, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
				if err != nil || parsed < 0 || parsed > 1 {
					quality = 0
				} else {
					quality = parsed
				}
			}
			if !specified[name] || quality > qualities[name] {
				qualities[name] = quality
				specified[name] = true
			}
		}
	}

	qualityFor := func(name string) float64 {
		if specified[name] {
			return qualities[name]
		}
		if specified["*"] {
			return qualities["*"]
		}
		return 0
	}
	brotliQuality := qualityFor("br")
	gzipQuality := qualityFor("gzip")
	if brotliQuality <= 0 && gzipQuality <= 0 {
		return ""
	}
	if brotliQuality >= gzipQuality {
		return "br"
	}
	return "gzip"
}

func addVaryHeader(header http.Header, value string) {
	for _, existing := range header.Values("Vary") {
		for _, item := range strings.Split(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(item), value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}
