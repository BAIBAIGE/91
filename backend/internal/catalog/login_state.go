package catalog

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// RecordLoginAttempt serializes the ban check, failure count and resulting ban.
// Successful credentials cannot clear a ban committed by another request.
func (c *Catalog) RecordLoginAttempt(ctx context.Context, ip string, successful bool, now time.Time, window time.Duration, threshold int) (bool, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var banned bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM banned_login_ips WHERE ip = ?)`, ip).Scan(&banned); err != nil {
		return false, err
	}
	if banned {
		return true, nil
	}
	if successful {
		if _, err := tx.ExecContext(ctx, `DELETE FROM login_failures WHERE ip = ?`, ip); err != nil {
			return false, err
		}
		return false, tx.Commit()
	}

	var count int
	var first int64
	err = tx.QueryRowContext(ctx, `SELECT failure_count, first_failed_at FROM login_failures WHERE ip = ?`, ip).Scan(&count, &first)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if errors.Is(err, sql.ErrNoRows) || first < now.Add(-window).UnixMilli() {
		count = 0
		first = now.UnixMilli()
	}
	count++
	if _, err := tx.ExecContext(ctx, `
INSERT INTO login_failures (ip, failure_count, first_failed_at) VALUES (?, ?, ?)
ON CONFLICT(ip) DO UPDATE SET failure_count = excluded.failure_count, first_failed_at = excluded.first_failed_at`, ip, count, first); err != nil {
		return false, err
	}
	banned = count >= threshold
	if banned {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO banned_login_ips (ip, reason, created_at) VALUES (?, ?, ?)`,
			ip, "too many failed login attempts", now.UnixMilli()); err != nil {
			return false, err
		}
	}
	return banned, tx.Commit()
}

// ResetLoginProtection is a server-start operation, not a database-open hook.
// Opening another connection or a backup must not release active login bans.
func (c *Catalog) ResetLoginProtection(ctx context.Context) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`DELETE FROM banned_login_ips`,
		`DELETE FROM login_failures`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}
