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

func TestExtractMetadataIgnoresExamplesOutsideModulePreamble(t *testing.T) {
	meta, err := ExtractMetadata(`
"""
Documentation example:
CRAWLER_PROTOCOL = get_protocol()
"""
CRAWLER_NAME = "示例爬虫"
CRAWLER_PROTOCOL = "crawler.v2"

def explain():
    CRAWLER_NAME = get_name()
    CRAWLER_PROTOCOL = get_protocol()
`)
	if err != nil {
		t.Fatalf("extract metadata: %v", err)
	}
	if meta.Name != "示例爬虫" || meta.Protocol != ProtocolV2 {
		t.Fatalf("metadata = %+v", meta)
	}
}

func TestExtractMetadataStillReadsModuleDeclarationAfterDefinition(t *testing.T) {
	meta, err := ExtractMetadata(`
CRAWLER_NAME = "兼容脚本"

def crawl():
    CRAWLER_PROTOCOL = get_protocol()

CRAWLER_PROTOCOL = "crawler.v2"
`)
	if err != nil {
		t.Fatalf("extract metadata: %v", err)
	}
	if meta.Protocol != ProtocolV2 {
		t.Fatalf("protocol = %q, want %q", meta.Protocol, ProtocolV2)
	}
}

func TestExtractMetadataLimitsCompatibilityScanToPreamble(t *testing.T) {
	source := "CRAWLER_NAME = \"兼容脚本\"\n" +
		strings.Repeat("# filler\n", maxMetadataPreambleLines) +
		"CRAWLER_PROTOCOL = get_protocol()\n"
	meta, err := ExtractMetadata(source)
	if err != nil {
		t.Fatalf("extract metadata: %v", err)
	}
	if meta.Protocol != ProtocolV1 {
		t.Fatalf("protocol = %q, want default %q", meta.Protocol, ProtocolV1)
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

func TestExtractMetadataRejectsProtocolDeclaredBelowPreamble(t *testing.T) {
	source := "CRAWLER_NAME = \"迟到声明\"\n" +
		strings.Repeat("# filler\n", maxMetadataPreambleLines) +
		"CRAWLER_PROTOCOL = \"crawler.v2\"\n"
	_, err := ExtractMetadata(source)
	if err == nil {
		t.Fatal("expected error: a v2 declaration below the preamble would run as v1")
	}
	if !strings.Contains(err.Error(), "CRAWLER_PROTOCOL") || !strings.Contains(err.Error(), ProtocolV1) {
		t.Fatalf("error = %v, want preamble guidance naming the effective protocol", err)
	}
}

func TestExtractMetadataAllowsRedundantProtocolBelowPreamble(t *testing.T) {
	source := "CRAWLER_NAME = \"重复声明\"\nCRAWLER_PROTOCOL = \"crawler.v2\"\n" +
		strings.Repeat("# filler\n", maxMetadataPreambleLines) +
		"CRAWLER_PROTOCOL = \"crawler.v2\"\n"
	meta, err := ExtractMetadata(source)
	if err != nil {
		t.Fatalf("extract metadata: %v", err)
	}
	if meta.Protocol != ProtocolV2 {
		t.Fatalf("protocol = %q, want %q", meta.Protocol, ProtocolV2)
	}
}

func TestExtractMetadataRejectsNameDeclaredBelowPreamble(t *testing.T) {
	source := strings.Repeat("# filler\n", maxMetadataPreambleLines) +
		"CRAWLER_NAME = \"迟到名字\"\n"
	_, err := ExtractMetadata(source)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "模块顶层") {
		t.Fatalf("error = %v, want preamble guidance instead of a missing-declaration message", err)
	}
}

func TestExtractMetadataIgnoresDocstringedProtocolBelowPreamble(t *testing.T) {
	source := "CRAWLER_NAME = \"文档脚本\"\n" +
		strings.Repeat("# filler\n", maxMetadataPreambleLines) +
		"\"\"\"\nCRAWLER_PROTOCOL = \"crawler.v2\"\n\"\"\"\n"
	meta, err := ExtractMetadata(source)
	if err != nil {
		t.Fatalf("extract metadata: %v", err)
	}
	if meta.Protocol != ProtocolV1 {
		t.Fatalf("protocol = %q, want %q", meta.Protocol, ProtocolV1)
	}
}
