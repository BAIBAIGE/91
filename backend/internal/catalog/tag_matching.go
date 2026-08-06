package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/video-site/backend/internal/fixedtags"
	"github.com/video-site/backend/internal/tagging"
)

func (c *Catalog) SetManualVideoTags(ctx context.Context, videoID string, labels []string) error {
	if _, err := c.GetVideo(ctx, videoID); err != nil {
		return err
	}
	return c.replaceVideoTags(ctx, videoID, labels, "manual", true, false)
}

// SetAutoVideoTags 用引擎结果覆盖视频的 auto/legacy 标签行；其余来源
// （crawler/series/propagated/manual）不受影响。
func (c *Catalog) SetAutoVideoTags(ctx context.Context, videoID string, labels []string) error {
	assignments := make([]TagAssignment, 0, len(labels))
	for _, label := range labels {
		assignments = append(assignments, TagAssignment{Label: label, Source: "auto"})
	}
	_, err := c.ReplaceAutoVideoTags(ctx, videoID, assignments)
	return err
}

// ---------- 匹配引擎入口 ----------

// Matcher 返回按当前标签池编译的匹配器；带版本号缓存，标签变更后自动重建。
func (c *Catalog) Matcher(ctx context.Context) (*tagging.Matcher, error) {
	version, err := c.tagRulesVersion(ctx)
	if err != nil {
		return nil, err
	}
	c.matcherMu.Lock()
	if c.matcher != nil && c.matcherVersion == version {
		m := c.matcher
		c.matcherMu.Unlock()
		return m, nil
	}
	c.matcherMu.Unlock()

	m, err := c.buildMatcher(ctx)
	if err != nil {
		return nil, err
	}
	c.matcherMu.Lock()
	c.matcher = m
	c.matcherVersion = version
	c.matcherMu.Unlock()
	return m, nil
}

func (c *Catalog) buildMatcher(ctx context.Context) (*tagging.Matcher, error) {
	builtinTagsEnabled, err := c.BuiltinTagsEnabled(ctx)
	if err != nil {
		return nil, err
	}
	avEnabled, err := c.avCodeMatchingEnabled(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := c.db.QueryContext(ctx,
		`SELECT label, aliases, COALESCE(match_rules, '{}'), source, COALESCE(origin, '') FROM tags ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tagRules []tagging.TagRule
	for rows.Next() {
		var label, aliasesJSON, rulesJSON, source, origin string
		if err := rows.Scan(&label, &aliasesJSON, &rulesJSON, &source, &origin); err != nil {
			return nil, err
		}
		origin = strings.ToLower(strings.TrimSpace(origin))
		if origin == avSeriesOrigin {
			continue
		}
		if !builtinTagsEnabled && normalizeTagSource(source) == fixedtags.SourceBuiltin {
			continue
		}
		if !avEnabled && strings.EqualFold(label, avTagLabel) {
			continue
		}
		var aliases []string
		_ = json.Unmarshal([]byte(aliasesJSON), &aliases)
		var rule tagging.Rule
		_ = json.Unmarshal([]byte(rulesJSON), &rule)
		tagRules = append(tagRules, tagging.TagRule{Label: label, Rule: effectiveRule(label, aliases, rule)})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return tagging.NewMatcher(tagRules), nil
}

// effectiveRule 计算标签的生效规则：无显式规则时按 label+legacy aliases 兜底；
// 有显式规则时按规则本身执行。AV 标签例外：它只按番号规则识别，避免
// "AV/JAV/番号" 这类普通描述误触发。
func effectiveRule(label string, aliases []string, rule tagging.Rule) tagging.Rule {
	if strings.EqualFold(label, avTagLabel) {
		prefixes := append([]string{}, rule.AVCodePrefixes...)
		prefixes = append(prefixes, aliases...)
		prefixes = tagging.CleanAVCodePrefixes(prefixes)
		if len(prefixes) == 0 {
			return tagging.Rule{}
		}
		return tagging.Rule{MatchAVCode: true, AVCodePrefixes: prefixes}
	}
	if rule.IsEmpty() {
		return tagging.RuleFromAliases(label, aliases)
	}
	return rule
}

func (c *Catalog) tagRulesVersion(ctx context.Context) (int64, error) {
	raw, err := c.GetSetting(ctx, settingTagRulesVersion, "0")
	if err != nil {
		return 0, err
	}
	version, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, nil
	}
	return version, nil
}

func (c *Catalog) bumpTagRulesVersion(ctx context.Context) error {
	_, err := c.db.ExecContext(ctx, `
INSERT INTO settings (key, value, updated_at) VALUES (?, '1', ?)
ON CONFLICT(key) DO UPDATE SET
  value = CAST(CAST(settings.value AS INTEGER) + 1 AS TEXT),
  updated_at = excluded.updated_at`, settingTagRulesVersion, time.Now().UnixMilli())
	return err
}

func bumpTagRulesVersionTx(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO settings (key, value, updated_at) VALUES (?, '1', ?)
ON CONFLICT(key) DO UPDATE SET
  value = CAST(CAST(settings.value AS INTEGER) + 1 AS TEXT),
  updated_at = excluded.updated_at`, settingTagRulesVersion, time.Now().UnixMilli())
	return err
}

// LookupTagLabel 查询某个标签是否已存在（大小写不敏感），返回库中的规范写法。
func (c *Catalog) LookupTagLabel(ctx context.Context, label string) (string, bool, error) {
	label = cleanTagLabel(label)
	if label == "" {
		return "", false, nil
	}
	tag, err := c.getTagByLabel(ctx, label)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return tag.Label, true, nil
}

// LookupUserSelectableTagLabel resolves a label that may be assigned explicitly
// by a user. Generated tags describe crawler provenance or derived AV series and
// are maintained by their respective pipelines; the AV umbrella is inferred
// from a code and is therefore not an upload choice either.
func (c *Catalog) LookupUserSelectableTagLabel(ctx context.Context, label string) (string, bool, error) {
	label = cleanTagLabel(label)
	if label == "" {
		return "", false, nil
	}
	tag, err := c.getTagByLabel(ctx, label)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !isUserSelectableTag(tag) {
		return "", false, nil
	}
	return tag.Label, true, nil
}

func isUserSelectableTag(tag Tag) bool {
	switch normalizeTagSource(tag.Source) {
	case "user":
		return true
	case "builtin":
		return !strings.EqualFold(tag.Label, avTagLabel)
	default:
		return false
	}
}

// MatchTags 对一段文本运行标签匹配，返回命中的标签名。
func (c *Catalog) MatchTags(ctx context.Context, text string) ([]string, error) {
	matcher, err := c.Matcher(ctx)
	if err != nil {
		return nil, err
	}
	return matcher.MatchLabels(text), nil
}

// MatchTagAssignments matches video metadata against the existing tag pool.
// The only tag definition it may create is an AV series label such as FC2PPV,
// and only while the built-in AV mechanism is enabled.
func (c *Catalog) MatchTagAssignments(ctx context.Context, title, fileName, author, dirName string) ([]TagAssignment, error) {
	matcher, err := c.Matcher(ctx)
	if err != nil {
		return nil, err
	}
	return c.matchTagAssignmentsWithMatcher(ctx, matcher, title, fileName, author, dirName)
}

func (c *Catalog) matchTagAssignmentsWithMatcher(ctx context.Context, matcher *tagging.Matcher, title, fileName, author, dirName string) ([]TagAssignment, error) {
	matches := matcher.Match(matchFields(title, fileName, author, dirName)...)
	out := make([]TagAssignment, 0, len(matches))
	seen := map[string]struct{}{}
	for _, m := range matches {
		seen[strings.ToLower(strings.TrimSpace(m.Label))] = struct{}{}
		out = append(out, TagAssignment{Label: m.Label, Source: "auto", Evidence: m.Evidence()})
	}
	series, evidence, err := c.matchAVSeriesAssignment(ctx, title, fileName, author, dirName)
	if err != nil {
		return nil, err
	}
	if series != "" {
		key := strings.ToLower(series)
		if _, ok := seen[key]; !ok {
			out = append(out, TagAssignment{Label: series, Source: "auto", Evidence: evidence})
		}
	}
	return out, nil
}

func (c *Catalog) matchAVSeriesAssignment(ctx context.Context, title, fileName, author, dirName string) (string, string, error) {
	enabled, err := c.avCodeMatchingEnabled(ctx)
	if err != nil || !enabled {
		return "", "", err
	}
	avCodes, err := c.avCodeMatcher(ctx)
	if err != nil {
		return "", "", err
	}
	for _, field := range matchFields(title, fileName, author, dirName) {
		code := avCodes.Find(field.Text)
		if code == "" {
			continue
		}
		series := avCodes.SeriesOf(code)
		if series == "" {
			continue
		}
		tag, err := c.ensureAVSeriesTag(ctx, series)
		if err != nil {
			return "", "", err
		}
		evidence := code
		if field.Name != "" {
			evidence = field.Name + ":" + code
		}
		return tag.Label, evidence, nil
	}
	return "", "", nil
}

func (c *Catalog) avCodeMatcher(ctx context.Context) (*tagging.AVCodeMatcher, error) {
	tag, err := c.getTagByLabel(ctx, avTagLabel)
	if err != nil {
		return nil, err
	}
	rule := effectiveRule(avTagLabel, tag.Aliases, tag.MatchRules)
	return tagging.NewAVCodeMatcher(rule.AVCodePrefixes), nil
}

func (c *Catalog) ensureTag(ctx context.Context, label string, aliases []string, source string) (Tag, error) {
	return c.ensureTagWithRules(ctx, label, aliases, tagging.Rule{}, source)
}

// EnsureTag ensures that a tag exists and returns its canonical database row.
func (c *Catalog) EnsureTag(ctx context.Context, label, source string) (Tag, error) {
	return c.ensureTag(ctx, label, nil, source)
}

// EnsureCrawlerTag ensures the crawler ownership tag exists. Crawler tags are
// source provenance, so they are not blocked by tags.auto_generate_enabled.
func (c *Catalog) EnsureCrawlerTag(ctx context.Context, label string) (Tag, error) {
	label = cleanTagLabel(label)
	tag, err := c.ensureTagWithRulesInternal(ctx, label, nil, tagging.Rule{}, "generated", false)
	if err != nil {
		return Tag{}, err
	}
	if err := c.markTagOrigin(ctx, tag.ID, "crawler"); err != nil {
		return Tag{}, err
	}
	tag.CrawlerOwned = true
	return tag, nil
}

func (c *Catalog) markTagOrigin(ctx context.Context, tagID int64, origin string) error {
	origin = strings.TrimSpace(strings.ToLower(origin))
	if origin != "crawler" && origin != avSeriesOrigin {
		origin = ""
	}
	res, err := c.db.ExecContext(ctx, `
UPDATE tags
   SET origin = ?, updated_at = ?
 WHERE id = ?
   AND COALESCE(origin, '') != ?`, origin, time.Now().UnixMilli(), tagID, origin)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		return c.bumpTagRulesVersion(ctx)
	}
	return nil
}

// EnsureCrawlerTagForVideo ensures a single crawler-owned video carries its
// crawler provenance tag. Unlike ordinary auto tags, this bypasses the
// auto-generation setting and does not skip manually curated videos.
func (c *Catalog) EnsureCrawlerTagForVideo(ctx context.Context, videoID, label string) (bool, error) {
	videoID = strings.TrimSpace(videoID)
	if videoID == "" {
		return false, errors.New("video id is required")
	}
	tag, err := c.EnsureCrawlerTag(ctx, label)
	if err != nil {
		return false, err
	}
	changed, labelAdded, err := c.upsertVideoTagAssignment(ctx, videoID, tag.ID, "crawler", "爬虫:"+tag.Label)
	if err != nil {
		return false, err
	}
	if labelAdded {
		if err := c.syncVideoTagsJSON(ctx, videoID, c.hasManualTags(ctx, videoID)); err != nil {
			return changed, err
		}
	}
	return changed, nil
}

// ensureTagWithRules 建标签（存在则复用）。规则只在两种情况下写入：
// 新建时、或已有行的 match_rules 为空时（升级回填）；不会覆盖管理员显式改过的规则。
func (c *Catalog) ensureTagWithRules(ctx context.Context, label string, aliases []string, rule tagging.Rule, source string) (Tag, error) {
	return c.ensureTagWithRulesInternal(ctx, label, aliases, rule, source, true)
}

func (c *Catalog) ensureTagWithRulesInternal(ctx context.Context, label string, aliases []string, rule tagging.Rule, source string, respectAutoGenerateSetting bool) (Tag, error) {
	label = cleanTagLabel(label)
	if label == "" {
		return Tag{}, errors.New("tag label is required")
	}
	if isAVCodePollutedLabel(label) {
		label = avTagLabel
		aliases = fixedtags.AliasesFor(avTagLabel)
		rule = avTagRule
		source = fixedtags.SourceBuiltin
	}
	if source == "" {
		source = "user"
	}
	source = normalizeTagSource(source)
	if source == "builtin" && !fixedtags.IsBuiltinLabel(label) {
		source = "generated"
	}
	if source == fixedtags.SourceBuiltin {
		enabled, err := c.BuiltinTagsEnabled(ctx)
		if err != nil {
			return Tag{}, err
		}
		if !enabled {
			return Tag{}, ErrBuiltinTagsDisabled
		}
	}
	if source == "generated" {
		tag, err := c.getTagByLabel(ctx, label)
		if err == nil {
			return tag, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return Tag{}, err
		}
		if respectAutoGenerateSetting {
			enabled, err := c.AutoGenerateTagsEnabled(ctx)
			if err != nil {
				return Tag{}, err
			}
			if !enabled {
				return Tag{}, ErrAutoTagGenerationDisabled
			}
		}
	}
	aliases = cleanAliases(aliases, label)
	aliasesJSON, _ := json.Marshal(aliases)
	rulesJSON, _ := json.Marshal(rule)
	now := time.Now().UnixMilli()
	res, err := c.db.ExecContext(ctx, `
INSERT OR IGNORE INTO tags (label, aliases, match_rules, source, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)`, label, string(aliasesJSON), string(rulesJSON), source, now, now)
	if err != nil {
		return Tag{}, err
	}
	inserted := false
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		inserted = true
	}
	changed := inserted
	if !inserted {
		if source == fixedtags.SourceBuiltin {
			res, err := c.db.ExecContext(ctx, `
UPDATE tags
   SET source = ?, updated_at = ?
 WHERE label = ? COLLATE NOCASE
   AND source != ?
   AND source != 'user'`,
				source, now, label, source)
			if err != nil {
				return Tag{}, err
			}
			if n, err := res.RowsAffected(); err == nil && n > 0 {
				changed = true
			}
		}
		if strings.EqualFold(label, avTagLabel) && source == fixedtags.SourceBuiltin {
			current, err := c.getTagByLabel(ctx, label)
			if err != nil {
				return Tag{}, err
			}
			legacyMissingPrefixes := current.MatchRules.IsEmpty() ||
				(current.MatchRules.MatchAVCode && len(current.MatchRules.AVCodePrefixes) == 0)
			if legacyMissingPrefixes {
				res, err := c.db.ExecContext(ctx, `
UPDATE tags
   SET match_rules = ?, updated_at = ?
 WHERE label = ? COLLATE NOCASE`,
					string(rulesJSON), now, label)
				if err != nil {
					return Tag{}, err
				}
				if n, err := res.RowsAffected(); err == nil && n > 0 {
					changed = true
				}
			}
		}
		if len(aliases) > 0 {
			res, err := c.db.ExecContext(ctx,
				`UPDATE tags SET aliases = ?, updated_at = ? WHERE label = ? COLLATE NOCASE AND COALESCE(aliases, '[]') != ?`,
				string(aliasesJSON), now, label, string(aliasesJSON))
			if err != nil {
				return Tag{}, err
			}
			if n, err := res.RowsAffected(); err == nil && n > 0 {
				changed = true
			}
		}
		if !rule.IsEmpty() {
			// 升级回填：已有行没有显式规则时补上默认规则。
			res, err := c.db.ExecContext(ctx, `
UPDATE tags SET match_rules = ?, updated_at = ?
 WHERE label = ? COLLATE NOCASE
   AND COALESCE(match_rules, '{}') IN ('', '{}', 'null')`,
				string(rulesJSON), now, label)
			if err != nil {
				return Tag{}, err
			}
			if n, err := res.RowsAffected(); err == nil && n > 0 {
				changed = true
			}
		}
	}
	if changed {
		if err := c.bumpTagRulesVersion(ctx); err != nil {
			return Tag{}, err
		}
	}
	if strings.EqualFold(label, avTagLabel) && (source == fixedtags.SourceBuiltin || source == "user") {
		if err := c.setAVCodeMatchingDisabled(ctx, false); err != nil {
			return Tag{}, err
		}
	}
	return c.getTagByLabel(ctx, label)
}

func (c *Catalog) ensureAVSeriesTag(ctx context.Context, series string) (Tag, error) {
	series = strings.ToUpper(cleanTagLabel(series))
	if series == "" {
		return Tag{}, errors.New("AV series tag label is required")
	}
	tag, err := c.ensureTagWithRulesInternal(ctx, series, nil, tagging.Rule{Keywords: []string{series}}, "generated", false)
	if err != nil {
		return Tag{}, err
	}
	if tag.Source == "generated" {
		if err := c.markTagOrigin(ctx, tag.ID, avSeriesOrigin); err != nil {
			return Tag{}, err
		}
	}
	return c.getTagByLabel(ctx, series)
}

func (c *Catalog) avCodeMatchingEnabled(ctx context.Context) (bool, error) {
	builtinTagsEnabled, err := c.BuiltinTagsEnabled(ctx)
	if err != nil || !builtinTagsEnabled {
		return false, err
	}
	disabled, err := c.avCodeMatchingDisabled(ctx)
	if err != nil || disabled {
		return false, err
	}
	if _, err := c.getTagByLabel(ctx, avTagLabel); errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, nil
}

func (c *Catalog) avCodeMatchingDisabled(ctx context.Context) (bool, error) {
	raw, err := c.GetSetting(ctx, settingAVCodeMatchingDisabled, "false")
	if err != nil {
		return false, err
	}
	return parseSettingBool(raw, false), nil
}

func (c *Catalog) setAVCodeMatchingDisabled(ctx context.Context, disabled bool) error {
	current, err := c.avCodeMatchingDisabled(ctx)
	if err != nil {
		return err
	}
	if current == disabled {
		return nil
	}
	value := "false"
	if disabled {
		value = "true"
	}
	if err := c.SetSetting(ctx, settingAVCodeMatchingDisabled, value); err != nil {
		return err
	}
	return c.bumpTagRulesVersion(ctx)
}

func setAVCodeMatchingDisabledTx(ctx context.Context, tx *sql.Tx, disabled bool) error {
	value := "false"
	if disabled {
		value = "true"
	}
	_, err := tx.ExecContext(ctx, `
INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
ON CONFLICT(key) DO UPDATE SET
  value = excluded.value,
  updated_at = excluded.updated_at`, settingAVCodeMatchingDisabled, value, time.Now().UnixMilli())
	return err
}
