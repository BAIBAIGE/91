package catalog

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
)

func setupTestCatalogForUsers(t *testing.T) *Catalog {
	t.Helper()
	dbPath := t.TempDir() + "/test.db"
	c, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c.Close()
		os.Remove(dbPath)
	})
	return c
}

func TestCreateAndGetUser(t *testing.T) {
	c := setupTestCatalogForUsers(t)
	ctx := context.Background()

	id, err := c.CreateUser(ctx, "testuser", "hashedpass", "user")
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	u, err := c.GetUserByUsername(ctx, "testuser")
	if err != nil {
		t.Fatal(err)
	}
	if u.Username != "testuser" {
		t.Fatalf("expected testuser, got %s", u.Username)
	}
	if u.Role != "user" {
		t.Fatalf("expected user role, got %s", u.Role)
	}
}

func TestListUsers(t *testing.T) {
	c := setupTestCatalogForUsers(t)
	ctx := context.Background()

	c.CreateUser(ctx, "user1", "pass1", "user")
	c.CreateUser(ctx, "user2", "pass2", "admin")

	users, err := c.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}
	admins, err := c.ListAdmins(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(admins) != 1 || admins[0].Username != "user2" ||
		admins[0].Password != "pass2" || admins[0].Role != "admin" {
		t.Fatalf("administrators = %#v", admins)
	}
}

func TestSetUserBanned(t *testing.T) {
	c := setupTestCatalogForUsers(t)
	ctx := context.Background()

	id, _ := c.CreateUser(ctx, "bantest", "pass", "user")
	c.SetUserBanned(ctx, id, true)

	u, _ := c.GetUserByID(ctx, id)
	if !u.Banned {
		t.Fatal("expected user to be banned")
	}
}

func TestListBannedIPs(t *testing.T) {
	c := setupTestCatalogForUsers(t)
	ctx := context.Background()

	c.BanLoginIP(ctx, "1.2.3.4", "test ban")
	ips, err := c.ListBannedLoginIPs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) != 1 {
		t.Fatalf("expected 1 banned IP, got %d", len(ips))
	}
}

func TestUnbanIP(t *testing.T) {
	c := setupTestCatalogForUsers(t)
	ctx := context.Background()

	c.BanLoginIP(ctx, "5.6.7.8", "test")
	c.UnbanLoginIP(ctx, "5.6.7.8")

	banned, _ := c.IsLoginIPBanned(ctx, "5.6.7.8")
	if banned {
		t.Fatal("expected IP to be unbanned")
	}
}

func TestUserMutationsReturnNoRowsForMissingUser(t *testing.T) {
	c := setupTestCatalogForUsers(t)
	ctx := context.Background()

	if err := c.SetUserBanned(ctx, 404, true); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("SetUserBanned error = %v, want sql.ErrNoRows", err)
	}
	if err := c.UpdateUserPassword(ctx, 404, "hash"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("UpdateUserPassword error = %v, want sql.ErrNoRows", err)
	}
	if err := c.DeleteUser(ctx, 404); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("DeleteUser error = %v, want sql.ErrNoRows", err)
	}
}
