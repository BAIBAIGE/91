package catalog

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

var ErrAdminAlreadyInitialized = errors.New("administrator setup already completed")

const adminInitializedSetting = "auth.admin_initialized"

const adminSetupRequiredSQL = `SELECT NOT EXISTS (SELECT 1 FROM users)
    AND NOT EXISTS (SELECT 1 FROM settings WHERE key = ?)`

// AdminSetupRequired never reopens setup after the last account is removed.
func (c *Catalog) AdminSetupRequired(ctx context.Context) (bool, error) {
	var required bool
	err := c.db.QueryRowContext(ctx, adminSetupRequiredSQL, adminInitializedSetting).Scan(&required)
	return required, err
}

// InitializeAdmin serializes first-run setup across requests and processes.
func (c *Catalog) InitializeAdmin(ctx context.Context, username, hashedPassword string) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var required bool
	if err := tx.QueryRowContext(ctx, adminSetupRequiredSQL, adminInitializedSetting).Scan(&required); err != nil {
		return err
	}
	if !required {
		return ErrAdminAlreadyInitialized
	}
	if _, err := createUser(ctx, tx, username, hashedPassword, "admin"); err != nil {
		return err
	}
	return tx.Commit()
}

func createUser(ctx context.Context, tx *sql.Tx, username, hashedPassword, role string) (int64, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return 0, errors.New("username is required")
	}
	if role == "" {
		role = "user"
	}
	now := time.Now().UnixMilli()
	res, err := tx.ExecContext(ctx,
		`INSERT INTO users (username, password, role, banned, created_at) VALUES (?, ?, ?, 0, ?)`,
		username, hashedPassword, role, now)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, '1', ?) ON CONFLICT(key) DO NOTHING`,
		adminInitializedSetting, now); err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (c *Catalog) migrateAdminSetup(ctx context.Context) error {
	_, err := c.db.ExecContext(ctx, `
INSERT INTO settings (key, value, updated_at)
SELECT ?, '1', ? WHERE EXISTS (SELECT 1 FROM users)
ON CONFLICT(key) DO NOTHING`, adminInitializedSetting, time.Now().UnixMilli())
	return err
}
