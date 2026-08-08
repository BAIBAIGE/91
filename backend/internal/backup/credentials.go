package backup

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

func redactSnapshotDatabase(ctx context.Context, databasePath string, retainAdministrators bool) error {
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
	statements := []string{`DELETE FROM admin_sessions`}
	if !retainAdministrators {
		statements = append(statements, `DELETE FROM users WHERE role = 'admin'`)
	}
	for _, statement := range statements {
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
