package catalog

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUpdateUserPasswordRevokesOnlyTargetSessions(t *testing.T) {
	c := setupTestCatalogForUsers(t)
	ctx := context.Background()
	id, err := c.CreateUser(ctx, "owner", "old-hash", "admin")
	if err != nil {
		t.Fatal(err)
	}
	otherID, err := c.CreateUser(ctx, "viewer", "other-hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetUserBanned(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	for token, userID := range map[string]int64{"target-one": id, "target-two": id, "other": otherID} {
		if err := c.CreateSession(ctx, token, time.Hour, userID); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.UpdateUserPassword(ctx, id, "new-hash"); err != nil {
		t.Fatal(err)
	}
	user, err := c.GetUserByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if user.Password != "new-hash" || !user.Banned || user.Username != "owner" || user.Role != "admin" {
		t.Fatal("password reset changed unrelated account fields")
	}
	for _, token := range []string{"target-one", "target-two", "other"} {
		valid, _, err := c.ValidateSession(ctx, token)
		if err != nil || valid != (token == "other") {
			t.Fatalf("token=%s valid=%v err=%v", token, valid, err)
		}
	}
}

func TestPasswordResetRollsBackWhenSessionRevocationFails(t *testing.T) {
	c := setupTestCatalogForUsers(t)
	ctx := context.Background()
	id, err := c.CreateUser(ctx, "owner", "old-hash", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.CreateSession(ctx, "old-session", time.Hour, id); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.Exec(`CREATE TRIGGER reject_session_delete BEFORE DELETE ON admin_sessions BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateUserPassword(ctx, id, "new-hash"); err == nil {
		t.Fatal("reset succeeded despite failed revocation")
	}
	user, err := c.GetUserByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if user.Password != "old-hash" {
		t.Fatal("failed reset changed the password")
	}
	if valid, _, err := c.ValidateSession(ctx, "old-session"); err != nil || !valid {
		t.Fatalf("failed reset changed the session: %v %v", valid, err)
	}
}

func TestPasswordResetCheckRunsInsideWriterReservation(t *testing.T) {
	c := setupTestCatalogForUsers(t)
	ctx := context.Background()
	id, err := c.CreateUser(ctx, "owner", "old-hash", "admin")
	if err != nil {
		t.Fatal(err)
	}
	var databasePath string
	if err := c.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&databasePath); err != nil {
		t.Fatal(err)
	}
	other, err := sql.Open("sqlite", databasePath+"?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	failure := errors.New("maintenance denied")
	called := false
	err = c.UpdateUserPasswordWithCheck(ctx, id, "new-hash", func() error {
		called = true
		if _, err := other.Exec(`UPDATE users SET password = 'competing-hash' WHERE id = ?`, id); err == nil {
			t.Error("check ran without a writer reservation")
		}
		return failure
	})
	if !called || !errors.Is(err, failure) {
		t.Fatalf("check called=%v err=%v", called, err)
	}
	user, err := c.GetUserByID(ctx, id)
	if err != nil || user.Password != "old-hash" {
		t.Fatal("rejected check changed password")
	}
}

func TestVerifiedSessionCannotOutlivePasswordReset(t *testing.T) {
	c := setupTestCatalogForUsers(t)
	ctx := context.Background()
	id, err := c.CreateUser(ctx, "owner", "old-hash", "admin")
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	if created, err := c.CreateVerifiedUserSession(ctx, "before", expires, id, "old-hash"); err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if err := c.UpdateUserPassword(ctx, id, "new-hash"); err != nil {
		t.Fatal(err)
	}
	if created, err := c.CreateVerifiedUserSession(ctx, "stale-login", expires, id, "old-hash"); err != nil || created {
		t.Fatalf("stale session created=%v err=%v", created, err)
	}
	if valid, _, err := c.ValidateSession(ctx, "before"); err != nil || valid {
		t.Fatalf("old session valid=%v err=%v", valid, err)
	}
	if created, err := c.CreateVerifiedUserSession(ctx, "new-login", expires, id, "new-hash"); err != nil || !created {
		t.Fatalf("new session created=%v err=%v", created, err)
	}
	if err := c.SetUserBanned(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if created, err := c.CreateVerifiedUserSession(ctx, "banned-login", expires, id, "new-hash"); err != nil || created {
		t.Fatalf("banned session created=%v err=%v", created, err)
	}
}

func TestOpenExistingReadsAndWritesDatabasePaths(t *testing.T) {
	for _, name := range []string{"accounts.db", "accounts #100% & 数据.db"} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), name)
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(`CREATE TABLE existing_only (value TEXT); INSERT INTO existing_only VALUES ('original')`); err != nil {
				t.Fatal(err)
			}

			c, err := OpenExisting(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			var value string
			if err := c.db.QueryRowContext(ctx, `SELECT value FROM existing_only`).Scan(&value); err != nil {
				t.Fatal(err)
			}
			if value != "original" {
				t.Fatalf("value=%q, want original", value)
			}
			if _, err := c.db.ExecContext(ctx, `UPDATE existing_only SET value = 'updated'`); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRowContext(ctx, `SELECT value FROM existing_only`).Scan(&value); err != nil {
				t.Fatal(err)
			}
			if value != "updated" {
				t.Fatalf("value=%q, want updated in the original database", value)
			}
		})
	}
}

func TestOpenExistingDoesNotCreateOrMigrateDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "missing.db")
	if c, err := OpenExisting(ctx, path); err == nil {
		c.Close()
		t.Fatal("opened missing database")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("missing database was created: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE existing_only (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	c, err := OpenExisting(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var count int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("opening database created tables: count=%d", count)
	}
	if _, err := c.ListUsers(ctx); err == nil {
		t.Fatal("maintenance silently created the users table")
	}
}
