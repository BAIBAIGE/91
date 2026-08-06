package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/video-site/backend/internal/fixedtags"
)

// BuiltinTagsEnabled reports whether the built-in tag pack is enabled. Older
// databases do not have the setting, so enabled remains the backwards-
// compatible default.
func (c *Catalog) BuiltinTagsEnabled(ctx context.Context) (bool, error) {
	raw, err := c.GetSetting(ctx, settingBuiltinTagsEnabled, "true")
	if err != nil {
		return false, err
	}
	return parseSettingBool(raw, true), nil
}

// SetBuiltinTagsEnabled changes the built-in tag pack as one catalog
// transaction. Disabling removes definitions, assignments, and AV-series tags;
// enabling restores the current built-in definitions. It returns whether the
// effective catalog state changed.
func (c *Catalog) SetBuiltinTagsEnabled(ctx context.Context, enabled bool) (bool, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	current, err := builtinTagsEnabledTx(ctx, tx)
	if err != nil {
		return false, err
	}
	changed := current != enabled

	if enabled {
		seeded, err := seedBuiltinTagPackTx(ctx, tx)
		if err != nil {
			return false, err
		}
		changed = changed || seeded
	} else {
		removed, err := removeBuiltinTagPackTx(ctx, tx)
		if err != nil {
			return false, err
		}
		changed = changed || removed
	}

	if err := setBuiltinTagsEnabledTx(ctx, tx, enabled); err != nil {
		return false, err
	}
	if changed {
		if err := bumpTagRulesVersionTx(ctx, tx); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return changed, nil
}

func builtinTagsEnabledTx(ctx context.Context, tx *sql.Tx) (bool, error) {
	var raw string
	err := tx.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = ?`, settingBuiltinTagsEnabled).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return parseSettingBool(raw, true), nil
}

func setBuiltinTagsEnabledTx(ctx context.Context, tx *sql.Tx, enabled bool) error {
	value := "false"
	if enabled {
		value = "true"
	}
	_, err := tx.ExecContext(ctx, `
INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
ON CONFLICT(key) DO UPDATE SET
  value = excluded.value,
  updated_at = excluded.updated_at`, settingBuiltinTagsEnabled, value, time.Now().UnixMilli())
	return err
}

func removeBuiltinTagPackTx(ctx context.Context, tx *sql.Tx) (bool, error) {
	var avSeriesTags int
	if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*)
  FROM tags
 WHERE lower(trim(COALESCE(source, ''))) = 'generated'
   AND lower(trim(COALESCE(origin, ''))) = ?`, avSeriesOrigin).Scan(&avSeriesTags); err != nil {
		return false, err
	}

	rows, err := tx.QueryContext(ctx, `
SELECT DISTINCT vt.video_id
  FROM video_tags vt
  JOIN tags t ON t.id = vt.tag_id
 WHERE lower(trim(COALESCE(t.source, ''))) = ?`, fixedtags.SourceBuiltin)
	if err != nil {
		return false, err
	}
	var videoIDs []string
	for rows.Next() {
		var videoID string
		if err := rows.Scan(&videoID); err != nil {
			rows.Close()
			return false, err
		}
		videoIDs = append(videoIDs, videoID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, err
	}
	if err := rows.Close(); err != nil {
		return false, err
	}

	if _, err := tx.ExecContext(ctx, `
DELETE FROM video_tags
 WHERE tag_id IN (
       SELECT id
         FROM tags
        WHERE lower(trim(COALESCE(source, ''))) = ?
 )`, fixedtags.SourceBuiltin); err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `
DELETE FROM tags
 WHERE lower(trim(COALESCE(source, ''))) = ?`, fixedtags.SourceBuiltin)
	if err != nil {
		return false, err
	}
	removedTags, _ := result.RowsAffected()

	avSeriesVideoIDs, err := cleanupGeneratedAVSeriesTagsTx(ctx, tx)
	if err != nil {
		return false, err
	}
	videoIDs = append(videoIDs, avSeriesVideoIDs...)

	avDisabled, err := avCodeMatchingDisabledTx(ctx, tx)
	if err != nil {
		return false, err
	}
	if err := setAVCodeMatchingDisabledTx(ctx, tx, true); err != nil {
		return false, err
	}

	for _, videoID := range uniqueStrings(videoIDs) {
		manual := hasManualTagsTx(ctx, tx, videoID)
		if err := syncVideoTagsJSONTx(ctx, tx, videoID, manual); err != nil {
			return false, err
		}
	}
	return removedTags > 0 || avSeriesTags > 0 || !avDisabled, nil
}

func avCodeMatchingDisabledTx(ctx context.Context, tx *sql.Tx) (bool, error) {
	var raw string
	err := tx.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = ?`, settingAVCodeMatchingDisabled).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return parseSettingBool(raw, false), nil
}

// seedBuiltinTagPack writes the current pack atomically. Existing user tags
// with the same label remain user-owned, and non-empty custom rules are kept.
func (c *Catalog) seedBuiltinTagPack(ctx context.Context) error {
	enabled, err := c.BuiltinTagsEnabled(ctx)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrBuiltinTagsDisabled
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	changed, err := seedBuiltinTagPackTx(ctx, tx)
	if err != nil {
		return err
	}
	if changed {
		if err := bumpTagRulesVersionTx(ctx, tx); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func seedBuiltinTagPackTx(ctx context.Context, tx *sql.Tx) (bool, error) {
	changed := false
	now := time.Now().UnixMilli()
	for _, definition := range fixedtags.All() {
		isAVTag := strings.EqualFold(definition.Label, avTagLabel)
		rule := definition.Rule
		if isAVTag {
			rule = avTagRule
		}
		aliases := cleanAliases(definition.Aliases, definition.Label)
		aliasesJSON, _ := json.Marshal(aliases)
		rulesJSON, _ := json.Marshal(rule)
		result, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO tags (label, aliases, match_rules, source, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)`, definition.Label, string(aliasesJSON), string(rulesJSON), definition.Source, now, now)
		if err != nil {
			return false, err
		}
		inserted, _ := result.RowsAffected()
		if inserted > 0 {
			changed = true
		} else {
			result, err = tx.ExecContext(ctx, `
UPDATE tags
   SET source = ?, updated_at = ?
 WHERE label = ? COLLATE NOCASE
   AND source != ?
   AND source != 'user'`, definition.Source, now, definition.Label, definition.Source)
			if err != nil {
				return false, err
			}
			if affected, _ := result.RowsAffected(); affected > 0 {
				changed = true
			}

			if isAVTag {
				current, err := getTagByLabelTxRaw(ctx, tx, definition.Label)
				if err != nil {
					return false, err
				}
				legacyMissingPrefixes := current.MatchRules.IsEmpty() ||
					(current.MatchRules.MatchAVCode && len(current.MatchRules.AVCodePrefixes) == 0)
				if legacyMissingPrefixes {
					result, err = tx.ExecContext(ctx, `
UPDATE tags SET match_rules = ?, updated_at = ? WHERE id = ?`,
						string(rulesJSON), now, current.ID)
					if err != nil {
						return false, err
					}
					if affected, _ := result.RowsAffected(); affected > 0 {
						changed = true
					}
				}
			}

			if len(aliases) > 0 {
				result, err = tx.ExecContext(ctx, `
UPDATE tags
   SET aliases = ?, updated_at = ?
 WHERE label = ? COLLATE NOCASE
   AND COALESCE(aliases, '[]') != ?`,
					string(aliasesJSON), now, definition.Label, string(aliasesJSON))
				if err != nil {
					return false, err
				}
				if affected, _ := result.RowsAffected(); affected > 0 {
					changed = true
				}
			}

			if !rule.IsEmpty() {
				result, err = tx.ExecContext(ctx, `
UPDATE tags
   SET match_rules = ?, updated_at = ?
 WHERE label = ? COLLATE NOCASE
   AND COALESCE(match_rules, '{}') IN ('', '{}', 'null')`,
					string(rulesJSON), now, definition.Label)
				if err != nil {
					return false, err
				}
				if affected, _ := result.RowsAffected(); affected > 0 {
					changed = true
				}
			}
		}

		if isAVTag {
			aliasesChanged, err := removeAVLegacyAliasesTx(ctx, tx)
			if err != nil {
				return false, err
			}
			changed = changed || aliasesChanged
		}
	}

	avDisabled, err := avCodeMatchingDisabledTx(ctx, tx)
	if err != nil {
		return false, err
	}
	if err := setAVCodeMatchingDisabledTx(ctx, tx, false); err != nil {
		return false, err
	}
	return changed || avDisabled, nil
}

func removeAVLegacyAliasesTx(ctx context.Context, tx *sql.Tx) (bool, error) {
	tag, err := getTagByLabelTxRaw(ctx, tx, avTagLabel)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	filtered := make([]string, 0, len(tag.Aliases))
	for _, alias := range tag.Aliases {
		if _, legacy := avLegacyAliases[strings.ToLower(strings.TrimSpace(alias))]; legacy {
			continue
		}
		filtered = append(filtered, alias)
	}
	if len(filtered) == len(tag.Aliases) {
		return false, nil
	}
	aliasesJSON, _ := json.Marshal(filtered)
	_, err = tx.ExecContext(ctx,
		`UPDATE tags SET aliases = ?, updated_at = ? WHERE id = ?`,
		string(aliasesJSON), time.Now().UnixMilli(), tag.ID)
	return err == nil, err
}
