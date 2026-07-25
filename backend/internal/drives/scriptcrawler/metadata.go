package scriptcrawler

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxCrawlerNameRunes      = 80
	maxMetadataPreambleLines = 200
)

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
	lines := strings.Split(source, "\n")
	preamble := min(len(lines), maxMetadataPreambleLines)

	meta := Metadata{Protocol: ProtocolV1}
	foundName := false
	tripleQuote := ""
	for _, line := range lines[:preamble] {
		key, value, ok := moduleLevelMetadataAssignment(line, &tripleQuote)
		if !ok {
			continue
		}
		switch key {
		case "CRAWLER_NAME":
			name, ok := parsePythonStringLiteral(value)
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
			protocol, ok := parsePythonStringLiteral(value)
			if !ok {
				return Metadata{}, errors.New(`CRAWLER_PROTOCOL 必须是字符串字面量，例如 CRAWLER_PROTOCOL = "crawler.v2"`)
			}
			protocol = strings.TrimSpace(protocol)
			if protocol != ProtocolV1 && protocol != ProtocolV2 {
				return Metadata{}, fmt.Errorf("不支持的 CRAWLER_PROTOCOL %q，目前支持 %s 和 %s", protocol, ProtocolV1, ProtocolV2)
			}
			meta.Protocol = protocol
		}
	}
	if err := rejectIgnoredMetadata(lines[preamble:], &tripleQuote, meta, foundName); err != nil {
		return Metadata{}, err
	}
	if !foundName {
		return Metadata{}, fmt.Errorf(`脚本必须在前 %d 行的模块顶层声明 CRAWLER_NAME，例如 CRAWLER_NAME = "示例爬虫"`, maxMetadataPreambleLines)
	}
	return meta, nil
}

// rejectIgnoredMetadata guards the preamble cutoff. A declaration below the
// cutoff is invisible to the scan above, so a script could silently run under
// a protocol it did not ask for. Only declarations that would actually change
// the outcome are rejected: legacy scripts keep computed assignments and
// examples further down the file without failing the import.
func rejectIgnoredMetadata(lines []string, tripleQuote *string, meta Metadata, foundName bool) error {
	for _, line := range lines {
		key, value, ok := moduleLevelMetadataAssignment(line, tripleQuote)
		if !ok {
			continue
		}
		literal, ok := parsePythonStringLiteral(value)
		if !ok {
			continue
		}
		literal = strings.TrimSpace(literal)
		switch key {
		case "CRAWLER_NAME":
			if foundName || literal == "" {
				continue
			}
			return fmt.Errorf("CRAWLER_NAME 必须声明在脚本前 %d 行的模块顶层", maxMetadataPreambleLines)
		case "CRAWLER_PROTOCOL":
			if literal == meta.Protocol || (literal != ProtocolV1 && literal != ProtocolV2) {
				continue
			}
			return fmt.Errorf("CRAWLER_PROTOCOL 必须声明在脚本前 %d 行的模块顶层，否则脚本会按 %s 运行",
				maxMetadataPreambleLines, meta.Protocol)
		}
	}
	return nil
}

// moduleLevelMetadataAssignment reports a top-level CRAWLER_NAME /
// CRAWLER_PROTOCOL assignment on one line, returning the raw right-hand side.
// Indented assignments belong to a function or class body and are ignored, as
// are comments and triple-quoted regions.
func moduleLevelMetadataAssignment(line string, tripleQuote *string) (string, string, bool) {
	code := stripPythonTripleQuotedText(line, tripleQuote)
	if code == "" || code[0] == ' ' || code[0] == '\t' {
		return "", "", false
	}
	trimmed := strings.TrimSpace(code)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", false
	}
	if !strings.HasPrefix(trimmed, "CRAWLER_NAME") && !strings.HasPrefix(trimmed, "CRAWLER_PROTOCOL") {
		return "", "", false
	}
	left, right, ok := strings.Cut(trimmed, "=")
	if !ok {
		return "", "", false
	}
	key := strings.TrimSpace(left)
	if key != "CRAWLER_NAME" && key != "CRAWLER_PROTOCOL" {
		return "", "", false
	}
	return key, right, true
}

// stripPythonTripleQuotedText removes module docstrings and other triple-quoted
// regions while preserving ordinary quoted literals used by metadata
// assignments. It is intentionally a small preamble lexer rather than a full
// Python parser.
func stripPythonTripleQuotedText(line string, tripleQuote *string) string {
	var out strings.Builder
	for i := 0; i < len(line); {
		if *tripleQuote != "" {
			end := strings.Index(line[i:], *tripleQuote)
			if end < 0 {
				return out.String()
			}
			i += end + len(*tripleQuote)
			*tripleQuote = ""
			continue
		}
		if line[i] == '#' {
			out.WriteString(line[i:])
			break
		}
		if i+3 <= len(line) && (line[i:i+3] == `"""` || line[i:i+3] == `'''`) {
			*tripleQuote = line[i : i+3]
			i += 3
			continue
		}
		if line[i] == '"' || line[i] == '\'' {
			quote := line[i]
			out.WriteByte(line[i])
			i++
			escaped := false
			for i < len(line) {
				ch := line[i]
				out.WriteByte(ch)
				i++
				if escaped {
					escaped = false
					continue
				}
				if ch == '\\' {
					escaped = true
					continue
				}
				if ch == quote {
					break
				}
			}
			continue
		}
		out.WriteByte(line[i])
		i++
	}
	return out.String()
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
