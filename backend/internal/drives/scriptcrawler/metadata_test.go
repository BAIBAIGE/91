package scriptcrawler

import (
	"strings"
	"testing"
)

func TestExtractMetadataReadsCrawlerName(t *testing.T) {
	meta, err := ExtractMetadata(`
# comment
CRAWLER_NAME = "示例爬虫"
`)
	if err != nil {
		t.Fatalf("extract metadata: %v", err)
	}
	if meta.Name != "示例爬虫" {
		t.Fatalf("name = %q", meta.Name)
	}
	if meta.Protocol != ProtocolV1 {
		t.Fatalf("protocol = %q, want %q", meta.Protocol, ProtocolV1)
	}
}

func TestExtractMetadataReadsCrawlerV2Protocol(t *testing.T) {
	meta, err := ExtractMetadata(`
CRAWLER_NAME = "示例爬虫"
CRAWLER_PROTOCOL = "crawler.v2"
`)
	if err != nil {
		t.Fatalf("extract metadata: %v", err)
	}
	if meta.Protocol != ProtocolV2 {
		t.Fatalf("protocol = %q, want %q", meta.Protocol, ProtocolV2)
	}
}

func TestExtractMetadataRejectsDynamicProtocol(t *testing.T) {
	_, err := ExtractMetadata(`
CRAWLER_NAME = "示例爬虫"
CRAWLER_PROTOCOL = "crawler." + "v2"
`)
	if err == nil || !strings.Contains(err.Error(), "CRAWLER_PROTOCOL") {
		t.Fatalf("error = %v, want CRAWLER_PROTOCOL guidance", err)
	}
}

func TestExtractMetadataRejectsUnsupportedProtocol(t *testing.T) {
	_, err := ExtractMetadata(`
CRAWLER_NAME = "示例爬虫"
CRAWLER_PROTOCOL = "crawler.v3"
`)
	if err == nil || !strings.Contains(err.Error(), "不支持") {
		t.Fatalf("error = %v, want unsupported protocol error", err)
	}
}

func TestExtractMetadataRejectsMissingCrawlerName(t *testing.T) {
	_, err := ExtractMetadata(`print("hello")`)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "CRAWLER_NAME") {
		t.Fatalf("error = %v, want CRAWLER_NAME guidance", err)
	}
}

func TestExtractMetadataRejectsEmptyCrawlerName(t *testing.T) {
	_, err := ExtractMetadata(`CRAWLER_NAME = "  "`)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "不能为空") {
		t.Fatalf("error = %v, want empty-name error", err)
	}
}
