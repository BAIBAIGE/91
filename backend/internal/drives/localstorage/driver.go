// Package localstorage exposes an existing server-side directory as a Drive.
package localstorage

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/video-site/backend/internal/drives"
)

const Kind = "localstorage"

const maxSTRMBytes = 64 * 1024

type Config struct {
	ID       string
	RootPath string
	// STRMAllowOutsideRoot 允许 .strm 指向存储根目录之外的本地路径。
	// 默认关闭：strm 等于可以让 /p/stream 读到服务器上的任意文件，只有
	// 管理员明确知道自己在做什么（例如 strm 库与 rclone 挂载目录分离）
	// 时才应打开。
	STRMAllowOutsideRoot bool
}

type Driver struct {
	id                   string
	rootPath             string
	strmAllowOutsideRoot bool
}

func New(c Config) *Driver {
	return &Driver{
		id:                   c.ID,
		rootPath:             c.RootPath,
		strmAllowOutsideRoot: c.STRMAllowOutsideRoot,
	}
}

func (d *Driver) Kind() string { return Kind }

func (d *Driver) ID() string { return d.id }

func (d *Driver) RootID() string { return "/" }

func (d *Driver) Init(context.Context) error {
	root, err := d.root()
	if err != nil {
		return err
	}
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("localstorage: stat root %q: %w%s", root, err, localStoragePathHint(d.rootPath))
	}
	if !info.IsDir() {
		return fmt.Errorf("localstorage: root is not a directory: %s", root)
	}
	return nil
}

func (d *Driver) List(ctx context.Context, dirID string) ([]drives.Entry, error) {
	dir, rel, err := d.pathForID(dirID)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]drives.Entry, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Symlinks can escape the configured root or create cycles. Keep the
		// local storage drive predictable by scanning real files/directories only.
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			continue
		}
		childRel := joinRel(rel, entry.Name())
		out = append(out, drives.Entry{
			ID:       encodeRel(childRel),
			Name:     entry.Name(),
			Size:     sizeForEntry(info),
			IsDir:    info.IsDir(),
			ParentID: idForRel(rel),
			ModTime:  info.ModTime(),
		})
	}
	return out, nil
}

func (d *Driver) Stat(ctx context.Context, fileID string) (*drives.Entry, error) {
	p, rel, err := d.pathForID(fileID)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	return &drives.Entry{
		ID:       idForRel(rel),
		Name:     filepath.Base(p),
		Size:     sizeForEntry(info),
		IsDir:    info.IsDir(),
		ParentID: idForRel(parentRel(rel)),
		ModTime:  info.ModTime(),
	}, nil
}

func (d *Driver) StreamURL(ctx context.Context, fileID string) (*drives.StreamLink, error) {
	p, _, err := d.pathForID(fileID)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if info.IsDir() || !info.Mode().IsRegular() {
		return nil, os.ErrNotExist
	}
	if strings.EqualFold(filepath.Ext(p), ".strm") {
		return d.streamURLFromSTRM(ctx, p)
	}
	if info.Size() <= 0 {
		return nil, os.ErrNotExist
	}
	return &drives.StreamLink{
		URL:     p,
		Expires: time.Now().Add(24 * time.Hour),
	}, nil
}

func (d *Driver) streamURLFromSTRM(ctx context.Context, strmPath string) (*drives.StreamLink, error) {
	target, err := readSTRMTarget(strmPath)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if filepath.IsAbs(target) {
		return d.localSTRMLink(strmPath, target)
	}
	u, err := url.Parse(target)
	if err == nil {
		switch strings.ToLower(u.Scheme) {
		case "http", "https":
			if u.Host == "" {
				return nil, fmt.Errorf("localstorage: invalid strm url %q", target)
			}
			return &drives.StreamLink{
				URL:     target,
				Expires: time.Now().Add(24 * time.Hour),
			}, nil
		case "file":
			if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
				return nil, fmt.Errorf("localstorage: unsupported strm file url host %q", u.Host)
			}
			return d.localSTRMLink(strmPath, u.Path)
		case "":
			// Local path below.
		default:
			return nil, fmt.Errorf("localstorage: unsupported strm target scheme %q", u.Scheme)
		}
	} else if strings.Contains(target, "://") {
		return nil, fmt.Errorf("localstorage: invalid strm url %q: %w", target, err)
	}
	return d.localSTRMLink(strmPath, target)
}

func readSTRMTarget(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxSTRMBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxSTRMBytes {
		return "", errors.New("localstorage: strm file is too large")
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if i == 0 {
			line = strings.TrimPrefix(line, "\ufeff")
		}
		line = strings.TrimSpace(line)
		if line != "" {
			return line, nil
		}
	}
	return "", errors.New("localstorage: empty strm target")
}

func (d *Driver) localSTRMLink(strmPath, target string) (*drives.StreamLink, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, errors.New("localstorage: empty strm target")
	}

	var p string
	if filepath.IsAbs(target) {
		p = filepath.Clean(target)
	} else {
		p = filepath.Join(filepath.Dir(strmPath), filepath.FromSlash(target))
	}
	p, err := filepath.Abs(p)
	if err != nil {
		return nil, err
	}
	root, err := d.root()
	if err != nil {
		return nil, err
	}
	realPath, within, err := realPathWithinRoot(root, p)
	if err != nil {
		return nil, err
	}
	if !within && !d.strmAllowOutsideRoot {
		return nil, errors.New("localstorage: strm target escapes root (enable strm_allow_outside_root to allow)")
	}
	if strings.EqualFold(filepath.Ext(p), ".strm") || strings.EqualFold(filepath.Ext(realPath), ".strm") {
		return nil, errors.New("localstorage: nested strm target is not supported")
	}
	info, err := os.Stat(realPath)
	if err != nil {
		return nil, err
	}
	if info.IsDir() || !info.Mode().IsRegular() || info.Size() <= 0 {
		return nil, os.ErrNotExist
	}
	return &drives.StreamLink{
		URL:     realPath,
		Expires: time.Now().Add(24 * time.Hour),
	}, nil
}

func (d *Driver) Remove(ctx context.Context, fileID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, rel, err := d.pathForID(fileID)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if rel == "" {
		return errors.New("localstorage: refusing to remove root")
	}
	info, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.IsDir() {
		return errors.New("localstorage: refusing to remove directory")
	}
	if !info.Mode().IsRegular() {
		return errors.New("localstorage: refusing to remove non-regular file")
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (d *Driver) root() (string, error) {
	raw := strings.TrimSpace(d.rootPath)
	if raw == "" {
		return "", errors.New("localstorage: empty path")
	}
	raw = os.ExpandEnv(raw)
	if strings.HasPrefix(raw, "~") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			switch {
			case raw == "~":
				raw = home
			case strings.HasPrefix(raw, "~/") || strings.HasPrefix(raw, `~\`):
				raw = filepath.Join(home, raw[2:])
			}
		}
	}
	return filepath.Abs(raw)
}

var _ drives.Remover = (*Driver)(nil)

func (d *Driver) pathForID(id string) (string, string, error) {
	root, err := d.root()
	if err != nil {
		return "", "", err
	}
	rel, err := decodeRel(id)
	if err != nil {
		return "", "", err
	}
	if rel == "" {
		return root, "", nil
	}
	p, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", "", err
	}
	if !pathWithinRoot(root, p) {
		return "", "", errors.New("localstorage: path escapes root")
	}
	if _, within, err := realPathWithinRoot(root, p); err != nil {
		return "", "", err
	} else if !within {
		return "", "", errors.New("localstorage: path escapes root")
	}
	return p, rel, nil
}

func pathWithinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

func realPathWithinRoot(root, path string) (string, bool, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false, err
	}
	realRoot, err = filepath.Abs(realRoot)
	if err != nil {
		return "", false, err
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false, err
	}
	realPath, err = filepath.Abs(realPath)
	if err != nil {
		return "", false, err
	}
	return realPath, pathWithinRoot(realRoot, realPath), nil
}

func localStoragePathHint(configured string) string {
	cwd, _ := os.Getwd()
	parts := []string{}
	if strings.TrimSpace(configured) != "" {
		parts = append(parts, fmt.Sprintf("configured=%q", strings.TrimSpace(configured)))
	}
	if cwd != "" {
		parts = append(parts, fmt.Sprintf("cwd=%q", cwd))
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		parts = append(parts, "docker=host paths must be bind-mounted into the container")
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func decodeRel(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" || id == "/" {
		return "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil {
		return "", fmt.Errorf("localstorage: invalid file id: %w", err)
	}
	rel := filepath.ToSlash(filepath.Clean(filepath.FromSlash(string(raw))))
	if rel == "." {
		return "", nil
	}
	if strings.HasPrefix(rel, "../") || rel == ".." || strings.HasPrefix(rel, "/") {
		return "", errors.New("localstorage: invalid relative path")
	}
	return rel, nil
}

func encodeRel(rel string) string {
	rel = filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
	if rel == "." || rel == "" {
		return "/"
	}
	return base64.RawURLEncoding.EncodeToString([]byte(rel))
}

func idForRel(rel string) string {
	if rel == "" {
		return "/"
	}
	return encodeRel(rel)
}

func joinRel(parent, name string) string {
	if parent == "" {
		return filepath.ToSlash(name)
	}
	return filepath.ToSlash(filepath.Join(filepath.FromSlash(parent), name))
}

func parentRel(rel string) string {
	if rel == "" {
		return ""
	}
	parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(rel)))
	if parent == "." {
		return ""
	}
	return parent
}

func sizeForEntry(info os.FileInfo) int64 {
	if info == nil || info.IsDir() {
		return 0
	}
	return info.Size()
}

var _ drives.Drive = (*Driver)(nil)
