package telegramstorage

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/proxy"
)

func TestLibraryPlaybackAndDeletionUseOpaqueIdentity(t *testing.T) {
	ctx := context.Background()
	cat, err := catalog.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	root := filepath.Join(t.TempDir(), "library")
	if err := os.Mkdir(root, 0750); err != nil {
		t.Fatal(err)
	}
	f, err := cat.ReserveTelegramLocalFile(ctx, catalog.TelegramLocalFile{FileID: "tg-opaque.media", Root: root, JobID: "job"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, f.FileID)
	if err := os.WriteFile(path, []byte("0123456789"), 0640); err != nil {
		t.Fatal(err)
	}
	drv := New(cat)
	reg := proxy.NewRegistry()
	reg.Set(drv.ID(), drv)
	p := proxy.New(reg)
	req := httptest.NewRequest("GET", "/stream", nil)
	req.Header.Set("Range", "bytes=2-5")
	rr := httptest.NewRecorder()
	p.ServeStream(rr, req, drv.ID(), f.FileID)
	if rr.Code != http.StatusPartialContent || rr.Body.String() != "2345" {
		t.Fatalf("range playback: %d %s", rr.Code, rr.Body.String())
	}
	for _, id := range []string{"../secret", path, "panel-status.json"} {
		if _, err := drv.StreamURL(ctx, id); err == nil {
			t.Fatalf("accepted unregistered path %s", id)
		}
	}
	if err := drv.Remove(ctx, f.FileID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("video file remains")
	}
	if _, err := cat.TelegramLocalFile(ctx, f.FileID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("storage mapping remains")
	}
	if err := drv.Remove(ctx, f.FileID); err != nil {
		t.Fatal("repeat deletion", err)
	}
}

func TestLibraryRejectsSymlinksAndTraversal(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")
	if err := os.Mkdir(root, 0750); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "PRIVATE_TOKEN")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.media")); err != nil {
		t.Skip(err)
	}
	for _, id := range []string{"../PRIVATE_TOKEN", outside, "linked.media", ".", ".."} {
		_, err := Path(catalog.TelegramLocalFile{Root: root, FileID: id})
		if err == nil {
			t.Fatalf("accepted %q", id)
		}
		if strings.Contains(err.Error(), "PRIVATE_TOKEN") {
			t.Fatal("path leaked into error")
		}
	}
}
