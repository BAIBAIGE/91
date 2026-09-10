package catalog

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func recordFailedLogin(t *testing.T, c *Catalog, ip string, now time.Time) bool {
	t.Helper()
	banned, err := c.RecordLoginAttempt(context.Background(), ip, false, now, 30*time.Minute, 3)
	if err != nil {
		t.Fatal(err)
	}
	return banned
}

func loginFailureCount(t *testing.T, c *Catalog, ip string) int {
	t.Helper()
	var count int
	if err := c.db.QueryRow(`SELECT COALESCE((SELECT failure_count FROM login_failures WHERE ip = ?), 0)`, ip).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestLoginProtectionStateSurvivesOpeningDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	const ip = "203.0.113.10"
	now := time.Unix(1700000000, 0)
	if recordFailedLogin(t, first, ip, now) {
		t.Fatal("first failure banned IP")
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if recordFailedLogin(t, second, ip, now) {
		t.Fatal("second failure banned IP")
	}
	if !recordFailedLogin(t, first, ip, now) {
		t.Fatal("shared failures did not ban IP")
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	second, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if count := loginFailureCount(t, second, ip); count != 3 {
		t.Fatalf("failures=%d, want 3", count)
	}
	if banned, err := second.IsLoginIPBanned(context.Background(), ip); err != nil || !banned {
		t.Fatalf("opening database cleared ban: banned=%v err=%v", banned, err)
	}
}

func TestLoginFailureWindow(t *testing.T) {
	for _, elapsed := range []time.Duration{30 * time.Minute, 30*time.Minute + time.Millisecond} {
		t.Run(elapsed.String(), func(t *testing.T) {
			c := setupTestCatalogForUsers(t)
			const ip = "203.0.113.11"
			now := time.Unix(1700000000, 123456789)
			recordFailedLogin(t, c, ip, now)
			recordFailedLogin(t, c, ip, now)
			banned := recordFailedLogin(t, c, ip, now.Add(elapsed))
			wantBan := elapsed == 30*time.Minute
			if banned != wantBan {
				t.Fatalf("banned=%v, want %v", banned, wantBan)
			}
			if !wantBan && loginFailureCount(t, c, ip) != 1 {
				t.Fatal("expired failure window was not reset")
			}
		})
	}
}

func TestSuccessfulLoginClearsOnlyUnbannedIPFailures(t *testing.T) {
	c := setupTestCatalogForUsers(t)
	ctx := context.Background()
	now := time.Now()
	const ip = "203.0.113.12"
	const otherIP = "203.0.113.13"
	recordFailedLogin(t, c, ip, now)
	recordFailedLogin(t, c, otherIP, now)
	if banned, err := c.RecordLoginAttempt(ctx, ip, true, now, 30*time.Minute, 3); err != nil || banned {
		t.Fatalf("successful attempt banned=%v err=%v", banned, err)
	}
	if loginFailureCount(t, c, ip) != 0 || loginFailureCount(t, c, otherIP) != 1 {
		t.Fatal("successful login cleared wrong failures")
	}
	if err := c.BanLoginIP(ctx, otherIP, "concurrent ban"); err != nil {
		t.Fatal(err)
	}
	if banned, err := c.RecordLoginAttempt(ctx, otherIP, true, now, 30*time.Minute, 3); err != nil || !banned {
		t.Fatalf("successful credentials bypassed ban: banned=%v err=%v", banned, err)
	}
	if loginFailureCount(t, c, otherIP) != 1 {
		t.Fatal("successful credentials cleared a banned IP's failures")
	}
}

func TestUnbanLoginIPClearsFailureCount(t *testing.T) {
	c := setupTestCatalogForUsers(t)
	ctx := context.Background()
	const ip = "203.0.113.14"
	now := time.Now()
	for i := 0; i < 3; i++ {
		recordFailedLogin(t, c, ip, now)
	}
	if err := c.UnbanLoginIP(ctx, ip); err != nil {
		t.Fatal(err)
	}
	if count := loginFailureCount(t, c, ip); count != 0 {
		t.Fatalf("failures after unban=%d", count)
	}
	if recordFailedLogin(t, c, ip, now) {
		t.Fatal("first failure after unban immediately banned IP")
	}
}

func TestLoginFailureAndBanCommitTogether(t *testing.T) {
	c := setupTestCatalogForUsers(t)
	ctx := context.Background()
	const ip = "203.0.113.15"
	now := time.Now()
	for i := 0; i < 2; i++ {
		recordFailedLogin(t, c, ip, now)
	}
	if _, err := c.db.Exec(`CREATE TRIGGER reject_ban BEFORE INSERT ON banned_login_ips BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RecordLoginAttempt(ctx, ip, false, now, 30*time.Minute, 3); err == nil {
		t.Fatal("ban insertion should fail")
	}
	if count := loginFailureCount(t, c, ip); count != 2 {
		t.Fatalf("failed transaction changed count to %d", count)
	}
	if banned, err := c.IsLoginIPBanned(ctx, ip); err != nil || banned {
		t.Fatalf("failed transaction banned=%v err=%v", banned, err)
	}
	if _, err := c.db.Exec(`DROP TRIGGER reject_ban`); err != nil {
		t.Fatal(err)
	}
	if !recordFailedLogin(t, c, ip, now) {
		t.Fatal("third committed failure did not ban IP")
	}
}

func TestLoginProtectionResetAndUnbanAreAtomic(t *testing.T) {
	for _, operation := range []string{"restart", "unban"} {
		t.Run(operation, func(t *testing.T) {
			c := setupTestCatalogForUsers(t)
			ctx := context.Background()
			const ip = "203.0.113.16"
			for i := 0; i < 3; i++ {
				recordFailedLogin(t, c, ip, time.Now())
			}
			if _, err := c.db.Exec(`CREATE TRIGGER reject_clear BEFORE DELETE ON login_failures BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
				t.Fatal(err)
			}
			var err error
			if operation == "restart" {
				err = c.ResetLoginProtection(ctx)
			} else {
				err = c.UnbanLoginIP(ctx, ip)
			}
			if err == nil {
				t.Fatal("failure cleanup should fail")
			}
			if banned, err := c.IsLoginIPBanned(ctx, ip); err != nil || !banned {
				t.Fatalf("failed cleanup removed ban: banned=%v err=%v", banned, err)
			}
			if loginFailureCount(t, c, ip) != 3 {
				t.Fatal("failed cleanup changed failure count")
			}
		})
	}
}

func TestResetLoginProtectionPreservesAccountsAndSessions(t *testing.T) {
	c := setupTestCatalogForUsers(t)
	ctx := context.Background()
	now := time.Now()
	id, err := c.CreateUser(ctx, "owner", "password-hash", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetUserBanned(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateSession(ctx, "existing-session", time.Hour, id); err != nil {
		t.Fatal(err)
	}
	if err := c.BanLoginIP(ctx, "203.0.113.17", "legacy ban"); err != nil {
		t.Fatal(err)
	}
	recordFailedLogin(t, c, "203.0.113.18", now)
	for i := 0; i < 3; i++ {
		recordFailedLogin(t, c, "203.0.113.19", now)
	}
	for i := 0; i < 2; i++ {
		if err := c.ResetLoginProtection(ctx); err != nil {
			t.Fatal(err)
		}
		ips, err := c.ListBannedLoginIPs(ctx)
		if err != nil || len(ips) != 0 {
			t.Fatalf("remaining bans=%v err=%v", ips, err)
		}
		var failures int
		if err := c.db.QueryRow(`SELECT COUNT(*) FROM login_failures`).Scan(&failures); err != nil {
			t.Fatal(err)
		}
		if failures != 0 {
			t.Fatalf("remaining failure rows=%d", failures)
		}
	}
	user, err := c.GetUserByID(ctx, id)
	if err != nil || !user.Banned || user.Password != "password-hash" {
		t.Fatalf("user changed: user=%+v err=%v", user, err)
	}
	if valid, userID, err := c.ValidateSession(ctx, "existing-session"); err != nil || !valid || userID != id {
		t.Fatalf("session changed: valid=%v user=%d err=%v", valid, userID, err)
	}
	if required, err := c.AdminSetupRequired(ctx); err != nil || required {
		t.Fatalf("setup marker changed: required=%v err=%v", required, err)
	}
	if recordFailedLogin(t, c, "203.0.113.19", now) {
		t.Fatal("reset left an old failure count")
	}
}

func TestConcurrentLoginFailuresAreCountedOnce(t *testing.T) {
	c := setupTestCatalogForUsers(t)
	ctx := context.Background()
	const attempts = 12
	const ip = "203.0.113.20"
	now := time.Now()
	results := make(chan error, attempts)
	start := make(chan struct{})
	for i := 0; i < attempts; i++ {
		go func() {
			<-start
			_, err := c.RecordLoginAttempt(ctx, ip, false, now, 30*time.Minute, attempts)
			results <- err
		}()
	}
	close(start)
	for i := 0; i < attempts; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if count := loginFailureCount(t, c, ip); count != attempts {
		t.Fatalf("count=%d, want %d", count, attempts)
	}
	if banned, err := c.IsLoginIPBanned(ctx, ip); err != nil || !banned {
		t.Fatalf("banned=%v err=%v", banned, err)
	}
	if _, err := c.RecordLoginAttempt(ctx, ip, false, now, 30*time.Minute, attempts); err != nil {
		t.Fatal(err)
	}
	if count := loginFailureCount(t, c, ip); count != attempts {
		t.Fatalf("banned request changed count to %d", count)
	}
}

func TestCanceledLoginAttemptDoesNotChangeState(t *testing.T) {
	c := setupTestCatalogForUsers(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.RecordLoginAttempt(ctx, "203.0.113.21", false, time.Now(), 30*time.Minute, 3); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if count := loginFailureCount(t, c, "203.0.113.21"); count != 0 {
		t.Fatalf("canceled attempt failures=%d", count)
	}
}
