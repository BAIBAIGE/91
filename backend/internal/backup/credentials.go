package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/config"
)

func redactSnapshotDatabase(ctx context.Context, databasePath string) error {
	location := &url.URL{
		Scheme: "file",
		Path:   filepath.ToSlash(databasePath),
	}
	query := location.Query()
	query.Set("mode", "rw")
	query.Set("_pragma", "busy_timeout(5000)")
	location.RawQuery = query.Encode()

	database, err := sql.Open("sqlite", location.String())
	if err != nil {
		return fmt.Errorf("backup: open SQLite snapshot for credential redaction: %w", err)
	}
	database.SetMaxOpenConns(1)
	defer func() {
		if database != nil {
			_ = database.Close()
		}
	}()

	var journalMode string
	if err := database.QueryRowContext(ctx, `PRAGMA journal_mode = DELETE`).Scan(&journalMode); err != nil {
		return fmt.Errorf("backup: configure SQLite snapshot journal: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(journalMode), "delete") {
		return fmt.Errorf("backup: SQLite snapshot journal mode is %q, want delete", journalMode)
	}

	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("backup: begin credential redaction: %w", err)
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`DELETE FROM admin_sessions`,
		`DELETE FROM users WHERE role = 'admin'`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("backup: redact SQLite snapshot credentials: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("backup: commit credential redaction: %w", err)
	}
	if err := database.Close(); err != nil {
		return fmt.Errorf("backup: close redacted SQLite snapshot: %w", err)
	}
	database = nil
	if err := verifySQLite(databasePath); err != nil {
		return fmt.Errorf("backup: verify redacted SQLite snapshot: %w", err)
	}
	return nil
}

func snapshotRedactedConfig(ctx context.Context, source, destination string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("backup: config source is not a regular file")
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	redacted, err := config.RedactAdminCredentials(data)
	if err != nil {
		return fmt.Errorf("backup: redact administrator config: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	mode := info.Mode().Perm()
	if mode == 0 {
		mode = 0o600
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, writeErr := output.Write(redacted)
	syncErr := output.Sync()
	closeErr := output.Close()
	for _, candidate := range []error{writeErr, syncErr, closeErr} {
		if candidate != nil {
			_ = os.Remove(destination)
			return candidate
		}
	}
	return nil
}

func restoreTargetAdministrators(
	ctx context.Context,
	tx *sql.Tx,
	targetAdmins []*catalog.User,
) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_sessions`); err != nil {
		return fmt.Errorf("backup: clear restored administrator sessions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE role = 'admin'`); err != nil {
		return fmt.Errorf("backup: remove source administrators: %w", err)
	}
	for _, admin := range targetAdmins {
		if admin == nil {
			continue
		}
		if _, err := tx.ExecContext(
			ctx,
			`DELETE FROM users WHERE username = ? COLLATE NOCASE`,
			admin.Username,
		); err != nil {
			return fmt.Errorf("backup: remove user conflicting with target administrator %q: %w", admin.Username, err)
		}
		banned := 0
		if admin.Banned {
			banned = 1
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO users (username, password, role, banned, created_at)
VALUES (?, ?, 'admin', ?, ?)`,
			admin.Username,
			admin.Password,
			banned,
			admin.CreatedAt,
		); err != nil {
			return fmt.Errorf("backup: restore target administrator %q: %w", admin.Username, err)
		}
	}
	return nil
}
