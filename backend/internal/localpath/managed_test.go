package localpath

import (
	"path/filepath"
	"testing"
)

func TestManagedReferencesFollowRootAndRejectEscapes(t *testing.T) {
	for _, root := range []string{filepath.Join(t.TempDir(), "previews"), filepath.Join(t.TempDir(), "moved previews")} {
		for _, reference := range []string{"video.mp4", "nested/video.mp4"} {
			got, ok := Managed(root, reference)
			if !ok || got != filepath.Join(root, reference) {
				t.Fatalf("Managed(%q, %q) = %q, %v", root, reference, got, ok)
			}
			relative, ok := ManagedRelative(root, got)
			if !ok || relative != reference {
				t.Fatalf("ManagedRelative(%q, %q) = %q, %v", root, got, relative, ok)
			}
		}
		for _, reference := range []string{"", ".", "..", "../private.mp4", "nested/../../private.mp4", filepath.Join(root+"-private", "secret.mp4")} {
			if path, ok := Managed(root, reference); ok {
				t.Fatalf("accepted escaping reference %q as %q", reference, path)
			}
		}
	}
}
