package catalog

import (
	"context"
	"database/sql"
	"errors"
)

// TelegramSettings is the retired database representation, read only for migration.
type TelegramSettings struct {
	Config   string
	BotToken string
	APIID    int64
	APIHash  string
	Version  string
}

func (c *Catalog) DeleteTelegramSettings(ctx context.Context) error {
	_, err := c.db.ExecContext(ctx, `DELETE FROM telegram_settings`)
	return err
}

func (c *Catalog) GetTelegramSettings(ctx context.Context) (TelegramSettings, error) {
	var s TelegramSettings
	err := c.db.QueryRowContext(ctx, `SELECT config,bot_token,api_id,api_hash,version FROM telegram_settings WHERE id=1`).Scan(&s.Config, &s.BotToken, &s.APIID, &s.APIHash, &s.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return TelegramSettings{}, nil
	}
	return s, err
}
