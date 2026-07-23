package scriptcrawler

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxCrawlerNameRunes = 80

const (
	ProtocolV1 = "crawler.v1"
	ProtocolV2 = "crawler.v2"
)

type Metadata struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
}

func ReadMetadata(scriptPath string) (Metadata, error) {
	scriptPath = strings.TrimSpace(scriptPath)
	if scriptPath == "" {
		return Metadata{}, errors.New("脚本路径为空")
	}
	if filepath.Ext(scriptPath) != ".py" {
		return Metadata{}, errors.New("目前只支持 .py 爬虫脚本")
	}
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		return Metadata{}, fmt.Errorf("读取脚本失败: %w", err)
	}
	return ExtractMetadata(string(data))
}

func ExtractMetadata(source string) (Metadata, error) {
	meta := Metadata{Protocol: ProtocolV1}
	foundName := false
	foundProtocol := false
	for _, line := range strings.Split(source, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.HasPrefix(trimmed, "CRAWLER_NAME") && !strings.HasPrefix(trimmed, "CRAWLER_PROTOCOL") {
			continue
		}
		left, right, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(left) {
		case "CRAWLER_NAME":
			name, ok := parsePythonStringLiteral(right)
			if !ok {
				return Metadata{}, errors.New(`CRAWLER_NAME 必须是字符串字面量，例如 CRAWLER_NAME = "示例爬虫"`)
			}
			name = strings.TrimSpace(name)
			if name == "" {
				return Metadata{}, errors.New("CRAWLER_NAME 不能为空")
			}
			if len([]rune(name)) > maxCrawlerNameRunes {
				return Metadata{}, fmt.Errorf("CRAWLER_NAME 不能超过 %d 个字符", maxCrawlerNameRunes)
			}
			meta.Name = name
			foundName = true
		case "CRAWLER_PROTOCOL":
			protocol, ok := parsePythonStringLiteral(right)
			if !ok {
				return Metadata{}, errors.New(`CRAWLER_PROTOCOL 必须是字符串字面量，例如 CRAWLER_PROTOCOL = "crawler.v2"`)
			}
			protocol = strings.TrimSpace(protocol)
			if protocol != ProtocolV1 && protocol != ProtocolV2 {
				return Metadata{}, fmt.Errorf("不支持的 CRAWLER_PROTOCOL %q，目前支持 %s 和 %s", protocol, ProtocolV1, ProtocolV2)
			}
			meta.Protocol = protocol
			foundProtocol = true
		}
	}
	if !foundName {
		return Metadata{}, errors.New(`脚本必须声明 CRAWLER_NAME，例如 CRAWLER_NAME = "示例爬虫"`)
	}
	if !foundProtocol {
		meta.Protocol = ProtocolV1
	}
	return meta, nil
}

func parsePythonStringLiteral(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", false
	}
	rawString := false
	for len(s) > 0 {
		switch s[0] {
		case 'r', 'R':
			rawString = true
			s = strings.TrimSpace(s[1:])
		case 'u', 'U', 'b', 'B':
			s = strings.TrimSpace(s[1:])
		default:
			goto parseQuote
		}
	}

parseQuote:
	if len(s) < 2 || (s[0] != '"' && s[0] != '\'') {
		return "", false
	}
	quote := s[0]
	var b strings.Builder
	escaped := false
	for i := 1; i < len(s); i++ {
		ch := s[i]
		if escaped {
			switch {
			case rawString:
				b.WriteByte('\\')
				b.WriteByte(ch)
			case ch == 'n':
				b.WriteByte('\n')
			case ch == 'r':
				b.WriteByte('\r')
			case ch == 't':
				b.WriteByte('\t')
			case ch == '\\' || ch == quote || ch == '"' || ch == '\'':
				b.WriteByte(ch)
			default:
				b.WriteByte(ch)
			}
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == quote {
			tail := strings.TrimSpace(s[i+1:])
			if tail != "" && !strings.HasPrefix(tail, "#") {
				return "", false
			}
			return b.String(), true
		}
		b.WriteByte(ch)
	}
	return "", false
}
