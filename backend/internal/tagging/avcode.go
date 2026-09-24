package tagging

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// 番号（AV code）识别。AV 默认只匹配内置车牌前缀；管理员可以通过
// AV 标签的前缀列表增删匹配用的车牌。
var (
	knownAVSeriesPrefixes = []string{
		"SSNI", "SSIS", "SNIS", "SOE", "IPX", "IPZ", "IPTD",
		"ABP", "ABW", "ONEZ", "MIDE", "MIDV", "MIAA", "MIMK",
		"ATID", "SHKD", "RBD", "FSDSS", "STAR", "MUD", "HND",
		"HMN", "WANZ", "CREAM", "VAGU", "JUL", "JUQ", "JUR",
		"OBA", "NKK", "JUFE", "FC2PPV", "SIRO", "300MIUM",
		"259LUXU", "CAWD", "SABA", "ZIZ", "PPPD", "EBOD",
		"EBWH", "BOBB", "CJOD", "PRED", "VEC", "IBW", "LBJ",
		"IMPA", "DDK", "MVG", "HUNT", "NTRD", "SDDE", "DASS",
		"MKMP", "BF", "BFDM",
	}
	defaultAVCodeMatcher    = NewAVCodeMatcher(knownAVSeriesPrefixes)
	subtitleTailCodePattern = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])([A-Z][A-Z0-9]{1,15})[-_ ]?(\d{2,8})$`)
)

var subtitleCodeNoisePrefixes = map[string]struct{}{
	"EP": {}, "FHD": {}, "HD": {}, "IMG": {}, "MOV": {}, "SAMPLE": {}, "VIDEO": {},
}

// AVCodeMatcher matches AV codes for one explicit prefix set.
type AVCodeMatcher struct {
	prefixes      []string
	codePattern   *regexp.Regexp
	inTextPattern *regexp.Regexp
}

// DefaultAVCodePrefixes returns the built-in AV code prefix list.
func DefaultAVCodePrefixes() []string {
	return append([]string(nil), knownAVSeriesPrefixes...)
}

// NormalizeAVCodePrefix converts a user-entered prefix to the canonical form.
func NormalizeAVCodePrefix(prefix string) string {
	prefix = strings.ToUpper(strings.TrimSpace(prefix))
	if prefix == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range prefix {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || unicode.IsSpace(r):
			continue
		default:
			return ""
		}
	}
	out := b.String()
	if len(out) < 2 || len(out) > 16 {
		return ""
	}
	return out
}

// CleanAVCodePrefixes normalizes, de-duplicates, and preserves input order.
func CleanAVCodePrefixes(prefixes []string) []string {
	out := make([]string, 0, len(prefixes))
	seen := map[string]struct{}{}
	for _, prefix := range prefixes {
		prefix = NormalizeAVCodePrefix(prefix)
		if prefix == "" {
			continue
		}
		if _, ok := seen[prefix]; ok {
			continue
		}
		seen[prefix] = struct{}{}
		out = append(out, prefix)
	}
	return out
}

func NewAVCodeMatcher(prefixes []string) *AVCodeMatcher {
	prefixes = CleanAVCodePrefixes(prefixes)
	m := &AVCodeMatcher{prefixes: prefixes}
	if len(prefixes) == 0 {
		return m
	}
	prefixPattern := buildAVSeriesPrefixPattern(prefixes)
	codePattern := `(?:` + prefixPattern + `)[-_ ]?\d{2,8}(?:[-_ ]?[A-Z0-9]{1,4}){0,2}`
	m.codePattern = regexp.MustCompile(`(?i)^` + codePattern + `$`)
	m.inTextPattern = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])(` + codePattern + `)(?:$|[^A-Za-z0-9])`)
	return m
}

func (m *AVCodeMatcher) Prefixes() []string {
	if m == nil {
		return nil
	}
	return append([]string(nil), m.prefixes...)
}

func (m *AVCodeMatcher) IsCode(label string) bool {
	label = strings.TrimSpace(label)
	return label != "" && m != nil && m.codePattern != nil && m.codePattern.MatchString(label)
}

func (m *AVCodeMatcher) Contains(text string) bool {
	return m != nil && m.inTextPattern != nil && m.inTextPattern.MatchString(text)
}

func (m *AVCodeMatcher) Find(text string) string {
	if m == nil || m.inTextPattern == nil {
		return ""
	}
	if matches := m.inTextPattern.FindStringSubmatch(text); len(matches) >= 2 {
		return strings.TrimSpace(matches[1])
	}
	return ""
}

func (m *AVCodeMatcher) SeriesOf(code string) string {
	if !m.IsCode(code) {
		return ""
	}
	normalized := normalizeCodeForPrefixMatch(code)
	for _, prefix := range sortedAVSeriesPrefixes(m.prefixes) {
		if prefix == "FC2PPV" {
			if hasSeriesPrefix(normalized, "FC2PPV") || hasSeriesPrefix(normalized, "FC2-PPV") {
				return prefix
			}
			continue
		}
		if hasSeriesPrefix(normalized, prefix) {
			return prefix
		}
	}
	return ""
}

// IsAVCode 判断一个独立字符串是否是内置车牌番号。
func IsAVCode(label string) bool {
	return defaultAVCodeMatcher.IsCode(label)
}

// ContainsAVCode 判断文本中是否出现内置车牌番号。
func ContainsAVCode(text string) bool {
	return defaultAVCodeMatcher.Contains(text)
}

// FindAVCode 返回文本中出现的第一个内置车牌番号（原样片段），没有则返回空串。
func FindAVCode(text string) string {
	return defaultAVCodeMatcher.Find(text)
}

// FindSubtitleAVCode returns a canonical code suitable for a subtitle search.
// It prefers the conservative built-in matcher, then accepts a generic code
// only when it appears at the end of a file name. The tail restriction keeps
// dates, resolutions and unrelated numbers in long titles out of lookups.
func FindSubtitleAVCode(text string) string {
	if code := FindAVCode(text); code != "" {
		return canonicalSubtitleAVCode(code, SeriesOf(code))
	}
	base := strings.TrimSpace(text)
	if ext := filepath.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	matches := subtitleTailCodePattern.FindStringSubmatch(strings.TrimSpace(base))
	if len(matches) != 3 {
		return ""
	}
	prefix := strings.ToUpper(matches[1])
	if _, noisy := subtitleCodeNoisePrefixes[prefix]; noisy {
		return ""
	}
	number := matches[2]
	if len(number) == 4 && (strings.HasPrefix(number, "19") || strings.HasPrefix(number, "20")) {
		return ""
	}
	return prefix + "-" + number
}

func canonicalSubtitleAVCode(code, series string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	compact := strings.NewReplacer("-", "", "_", "", " ", "").Replace(code)
	prefix := strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToUpper(series))
	if prefix != "" && strings.HasPrefix(compact, prefix) && len(compact) > len(prefix) {
		return prefix + "-" + compact[len(prefix):]
	}
	return strings.Trim(strings.NewReplacer("_", "-", " ", "-").Replace(code), "-")
}

// SeriesOf 从一个内置车牌番号中提取"车牌前缀"（系列名），统一为大写。
func SeriesOf(code string) string {
	return defaultAVCodeMatcher.SeriesOf(code)
}

// SeriesInText 提取文本中第一个内置车牌番号的系列前缀。
func SeriesInText(text string) string {
	return SeriesOf(FindAVCode(text))
}

func buildAVSeriesPrefixPattern(prefixes []string) string {
	prefixes = sortedAVSeriesPrefixes(prefixes)
	parts := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		if prefix == "FC2PPV" {
			parts = append(parts, `FC2[-_ ]?PPV`)
			continue
		}
		parts = append(parts, regexp.QuoteMeta(prefix))
	}
	return strings.Join(parts, "|")
}

func sortedAVSeriesPrefixes(prefixes []string) []string {
	prefixes = CleanAVCodePrefixes(prefixes)
	sort.Slice(prefixes, func(i, j int) bool {
		if len(prefixes[i]) == len(prefixes[j]) {
			return prefixes[i] < prefixes[j]
		}
		return len(prefixes[i]) > len(prefixes[j])
	})
	return prefixes
}

func normalizeCodeForPrefixMatch(code string) string {
	normalized := strings.ToUpper(strings.TrimSpace(code))
	normalized = strings.NewReplacer("_", "-", " ", "-").Replace(normalized)
	for strings.Contains(normalized, "--") {
		normalized = strings.ReplaceAll(normalized, "--", "-")
	}
	return normalized
}

func hasSeriesPrefix(code, prefix string) bool {
	if !strings.HasPrefix(code, prefix) {
		return false
	}
	if len(code) == len(prefix) {
		return false
	}
	next := code[len(prefix)]
	return next == '-' || (next >= '0' && next <= '9')
}
