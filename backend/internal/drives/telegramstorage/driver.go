// Package telegramstorage serves library files on the Bot API shared volume.
// Only catalogued opaque IDs are accepted; callers cannot address Bot API files.
package telegramstorage

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/drives"
)

const DriveID = catalog.TelegramLocalDriveID

type Driver struct{ cat *catalog.Catalog }

func New(cat *catalog.Catalog) *Driver       { return &Driver{cat: cat} }
func (d *Driver) Kind() string               { return DriveID }
func (d *Driver) ID() string                 { return DriveID }
func (d *Driver) RootID() string             { return "" }
func (d *Driver) Init(context.Context) error { return nil }
func (d *Driver) List(context.Context, string) ([]drives.Entry, error) {
	return nil, drives.ErrNotSupported
}

// Path rejects symlinks and returns only a reserved library file. Missing files
// retain their valid path so deletion and interrupted acquisitions are idempotent.
func Path(f catalog.TelegramLocalFile) (string, error) {
	if !filepath.IsAbs(f.Root) || filepath.Base(f.Root) != "library" || f.FileID == "" || filepath.Base(f.FileID) != f.FileID || strings.ContainsAny(f.FileID, "/\\\x00") || f.FileID == "." || f.FileID == ".." {
		return "", errors.New("TG 视频路径无效")
	}
	root, err := filepath.EvalSymlinks(f.Root)
	if err != nil {
		return "", errors.New("TG 视频存储目录不可用")
	}
	if filepath.Clean(root) != filepath.Clean(f.Root) {
		return "", errors.New("TG 视频存储目录不能包含符号链接")
	}
	path := filepath.Join(root, f.FileID)
	info, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("无法检查 TG 视频文件")
	}
	if err == nil && !info.Mode().IsRegular() {
		return "", errors.New("TG 视频不是普通文件")
	}
	return path, nil
}
func (d *Driver) LocalPath(ctx context.Context, id string) (string, error) {
	f, err := d.cat.TelegramLocalFile(ctx, id)
	if err != nil {
		return "", os.ErrNotExist
	}
	return Path(f)
}
func (d *Driver) Stat(ctx context.Context, id string) (*drives.Entry, error) {
	path, err := d.LocalPath(ctx, id)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, os.ErrNotExist
	}
	return &drives.Entry{ID: id, Name: id, Size: info.Size(), ModTime: info.ModTime()}, nil
}
func (d *Driver) StreamURL(ctx context.Context, id string) (*drives.StreamLink, error) {
	path, err := d.LocalPath(ctx, id)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return nil, os.ErrNotExist
	}
	return &drives.StreamLink{URL: path, Expires: time.Now().Add(time.Hour)}, nil
}
func (d *Driver) Remove(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := d.cat.TelegramLocalFile(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.New("无法读取 TG 视频位置")
	}
	path, err := Path(f)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("无法删除 TG 视频，请检查共享目录写入权限")
	}
	return d.cat.DeleteTelegramLocalFile(ctx, id)
}
