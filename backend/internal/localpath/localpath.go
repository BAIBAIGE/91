// Package localpath centralizes resolution and containment checks for
// application-managed files.
package localpath

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var ErrEmptyPath = errors.New("local path is empty")

// Resolve converts value to a clean absolute path. Relative values are
// resolved against baseDir instead of whichever directory a later caller
// happens to use.
func Resolve(baseDir, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ErrEmptyPath
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value), nil
	}
	baseDir = strings.TrimSpace(baseDir)
	if baseDir == "" {
		return "", ErrEmptyPath
	}
	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	return filepath.Clean(filepath.Join(baseAbs, value)), nil
}

// Within resolves root and candidate against the current working directory,
// then returns candidate's absolute path only when it is root itself or a
// descendant. filepath.Rel supplies filesystem-aware boundary semantics, so a
// sibling such as "previews-private" is not mistaken for "previews".
//
// Runtime roots should normally already be absolute. Resolving a relative
// candidate here keeps database rows written by older releases readable.
func Within(root, candidate string) (string, bool) {
	_, candidateAbs, relative, ok := resolveWithin(root, candidate)
	if !ok {
		return "", false
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return "", false
	}
	return candidateAbs, true
}

// RelativeWithin returns candidate relative to root using the same
// normalization and boundary rules as Within.
func RelativeWithin(root, candidate string) (string, bool) {
	_, _, relative, ok := resolveWithin(root, candidate)
	if !ok || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return "", false
	}
	return relative, true
}

func resolveWithin(root, candidate string) (string, string, string, bool) {
	workingDir, err := os.Getwd()
	if err != nil {
		return "", "", "", false
	}
	rootAbs, err := Resolve(workingDir, root)
	if err != nil {
		return "", "", "", false
	}
	candidateAbs, err := Resolve(workingDir, candidate)
	if err != nil {
		return "", "", "", false
	}
	relative, err := filepath.Rel(rootAbs, candidateAbs)
	if err != nil {
		return "", "", "", false
	}
	return rootAbs, candidateAbs, relative, true
}
