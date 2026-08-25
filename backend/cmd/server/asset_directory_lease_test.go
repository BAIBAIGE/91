package main

import (
	"strings"
	"testing"
)

func TestAssetDirectoryLeaseRejectsSharedWritersAndReleasesOnClose(t *testing.T) {
	assetDir := t.TempDir()
	first, err := acquireAssetDirectoryLease(assetDir, "/data/production.db")
	if err != nil {
		t.Fatalf("acquire first lease: %v", err)
	}

	if _, err := acquireAssetDirectoryLease(assetDir, "/tmp/demo.db"); err == nil {
		t.Fatal("second lease unexpectedly acquired shared asset directory")
	} else if !strings.Contains(err.Error(), "already owned by another server") ||
		!strings.Contains(err.Error(), "production.db") {
		t.Fatalf("second lease error = %v", err)
	}

	if err := first.Close(); err != nil {
		t.Fatalf("release first lease: %v", err)
	}
	second, err := acquireAssetDirectoryLease(assetDir, "/tmp/demo.db")
	if err != nil {
		t.Fatalf("acquire lease after release: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("release second lease: %v", err)
	}
}
