package catalog

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"
)

type poolHealthConnection interface {
	driver.SessionResetter
	driver.Validator
}

func TestSQLiteConnectionsExposePoolHealthChecks(t *testing.T) {
	cat, err := Open(t.TempDir() + "/catalog.db")
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })

	conn, err := cat.db.Conn(context.Background())
	if err != nil {
		t.Fatalf("reserve connection: %v", err)
	}
	defer conn.Close()

	err = conn.Raw(func(raw any) error {
		health, ok := raw.(poolHealthConnection)
		if !ok {
			return errors.New("sqlite connection does not implement database/sql pool health checks")
		}
		if !health.IsValid() {
			return errors.New("new sqlite connection is unexpectedly invalid")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCanceledSQLiteQueryDoesNotPoisonNextQuery(t *testing.T) {
	cat, err := Open(t.TempDir() + "/catalog.db")
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })
	cat.db.SetMaxOpenConns(1)
	cat.db.SetMaxIdleConns(1)

	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(5*time.Millisecond, cancel)
	var sum int64
	err = cat.db.QueryRowContext(ctx, `
WITH RECURSIVE numbers(n) AS (
  VALUES(0)
  UNION ALL
  SELECT n + 1 FROM numbers WHERE n < 100000000
)
SELECT sum(n) FROM numbers`).Scan(&sum)
	timer.Stop()
	cancel()
	if err == nil {
		t.Fatal("long-running query completed before cancellation")
	}

	var got int
	if err := cat.db.QueryRowContext(context.Background(), `SELECT 1`).Scan(&got); err != nil {
		t.Fatalf("query after cancellation: %v", err)
	}
	if got != 1 {
		t.Fatalf("query after cancellation = %d, want 1", got)
	}
}
