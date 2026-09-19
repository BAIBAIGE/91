package telegram

import (
	"errors"
	"path"
	"path/filepath"
	"strings"
)

// Bot API paths belong to the server's filesystem namespace. Translate only a
// validated relative suffix; openCachedFile then checks local symlink boundaries.
func mapAPIFilePath(apiRoot, localRoot, filename string) (string, error) {
	if !path.IsAbs(apiRoot) || !filepath.IsAbs(localRoot) || !path.IsAbs(filename) || path.Clean(filename) != filename || strings.ContainsAny(filename, "\\\x00") {
		return "", errors.New("invalid Bot API file path")
	}
	prefix := strings.TrimRight(path.Clean(apiRoot), "/") + "/"
	if !strings.HasPrefix(filename, prefix) {
		return "", errors.New("file outside Bot API directory")
	}
	relative := strings.TrimPrefix(filename, prefix)
	if relative == "" || !filepath.IsLocal(filepath.FromSlash(relative)) {
		return "", errors.New("invalid Bot API relative path")
	}
	return filepath.Join(localRoot, filepath.FromSlash(relative)), nil
}
