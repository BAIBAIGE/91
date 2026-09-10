package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

// OpenExisting opens a maintenance connection without creating or migrating
// the database or changing its journal mode.
func OpenExisting(ctx context.Context, path string) (*Catalog, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("database is not a regular file: %s", absolute)
	}
	location := &url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	query := location.Query()
	query.Set("mode", "rw")
	query.Set("_pragma", "busy_timeout(5000)")
	query.Set("_txlock", "immediate")
	location.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", location.String())
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Catalog{db: db}, nil
}
