package catalog

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestAdminSetupRemainsClosedAfterAllAccountsAreDeleted(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "catalog.db")
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if required, err := c.AdminSetupRequired(ctx); err != nil || !required {
		t.Fatalf("initial setup required=%v err=%v", required, err)
	}
	if err := c.InitializeAdmin(ctx, "owner", "hashed-password"); err != nil {
		t.Fatal(err)
	}
	u, err := c.GetUserByUsername(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetUserBanned(ctx, u.ID, true); err != nil {
		t.Fatal(err)
	}
	if required, err := c.AdminSetupRequired(ctx); err != nil || required {
		t.Fatalf("banned admin setup required=%v err=%v", required, err)
	}
	if err := c.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	c, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if required, err := c.AdminSetupRequired(ctx); err != nil || required {
		t.Fatalf("restarted setup required=%v err=%v", required, err)
	}
	if err := c.InitializeAdmin(ctx, "replacement", "hash"); !errors.Is(err, ErrAdminAlreadyInitialized) {
		t.Fatalf("reinitialization error=%v", err)
	}
}

func TestAdminSetupBackfillsExistingUsers(t *testing.T) {
	ctx := context.Background()
	for _, role := range []string{"admin", "user"} {
		t.Run(role, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "catalog.db")
			c, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			// Simulate a pre-migration database without the initialization marker.
			if _, err := c.db.Exec(`INSERT INTO users (username, password, role, banned, created_at) VALUES ('legacy', 'hash', ?, 1, 0)`, role); err != nil {
				t.Fatal(err)
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			c, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if _, err := c.db.Exec(`DELETE FROM users`); err != nil {
				t.Fatal(err)
			}
			if required, err := c.AdminSetupRequired(ctx); err != nil || required {
				t.Fatalf("setup required=%v err=%v", required, err)
			}
		})
	}
}

func TestInitializeAdminIsAtomic(t *testing.T) {
	c := setupTestCatalogForUsers(t)
	ctx := context.Background()
	if _, err := c.db.Exec(`CREATE TRIGGER reject_setup_marker BEFORE INSERT ON settings BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := c.InitializeAdmin(ctx, "owner", "hash"); err == nil {
		t.Fatal("setup succeeded despite marker failure")
	}
	if count, err := c.CountUsers(ctx); err != nil || count != 0 {
		t.Fatalf("partial setup users=%d err=%v", count, err)
	}
	if required, err := c.AdminSetupRequired(ctx); err != nil || !required {
		t.Fatalf("failed setup required=%v err=%v", required, err)
	}
	if _, err := c.db.Exec(`DROP TRIGGER reject_setup_marker`); err != nil {
		t.Fatal(err)
	}
	if err := c.InitializeAdmin(ctx, "owner", "hash"); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentInitializeAdminCreatesOneAccount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	start := make(chan struct{})
	results := make(chan error, 2)
	for i, c := range []*Catalog{first, second} {
		go func(c *Catalog, username string) {
			<-start
			results <- c.InitializeAdmin(context.Background(), username, "hash")
		}(c, []string{"first", "second"}[i])
	}
	close(start)
	successes := 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrAdminAlreadyInitialized) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful setups=%d", successes)
	}
	if count, err := first.CountUsers(context.Background()); err != nil || count != 1 {
		t.Fatalf("users=%d err=%v", count, err)
	}
}
