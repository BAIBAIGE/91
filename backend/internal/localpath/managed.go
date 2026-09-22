package localpath

import "path/filepath"

// Managed resolves a persisted file reference against its owning directory.
// Relative references are portable between deployments. Absolute references
// are accepted only inside root while legacy catalogs are being converted.
func Managed(root, reference string) (string, bool) {
	if reference == "" || root == "" {
		return "", false
	}
	absolute, err := Resolve(root, reference)
	if err != nil {
		return "", false
	}
	relative, ok := RelativeWithin(root, absolute)
	if !ok || relative == "." {
		return "", false
	}
	return absolute, true
}

// ManagedRelative returns the portable reference for a managed file.
func ManagedRelative(root, reference string) (string, bool) {
	absolute, ok := Managed(root, reference)
	if !ok {
		return "", false
	}
	relative, ok := RelativeWithin(root, absolute)
	return filepath.ToSlash(relative), ok
}
