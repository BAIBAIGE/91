package localpath

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveUsesExplicitBaseDirectory(t *testing.T) {
	base := t.TempDir()

	got, err := Resolve(base, "./data/previews")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(base, "data", "previews")
	if got != want {
		t.Fatalf("Resolve() = %q, want %q", got, want)
	}
}

func TestResolvePreservesAbsolutePath(t *testing.T) {
	want := filepath.Join(t.TempDir(), "previews")

	got, err := Resolve("/ignored", want)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Resolve() = %q, want %q", got, want)
	}
}

func TestWithinAcceptsEquivalentRelativeAndAbsolutePaths(t *testing.T) {
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "previews")
	candidate := filepath.Join(root, "video.mp4")
	relativeRoot, err := filepath.Rel(workingDir, root)
	if err != nil {
		t.Fatal(err)
	}

	got, ok := Within(relativeRoot, candidate)
	if !ok || got != candidate {
		t.Fatalf("Within() = %q, %v; want %q, true", got, ok, candidate)
	}
}

func TestWithinRejectsSiblingWithSharedPrefix(t *testing.T) {
	root := filepath.Join(t.TempDir(), "previews")
	sibling := filepath.Join(filepath.Dir(root), "previews-private", "secret.mp4")

	if got, ok := Within(root, sibling); ok {
		t.Fatalf("Within() = %q, true; want rejection", got)
	}
}

func TestRelativeWithinUsesSameBoundaryRules(t *testing.T) {
	root := filepath.Join(t.TempDir(), "previews")
	candidate := filepath.Join(root, "nested", "video.mp4")

	got, ok := RelativeWithin(root, candidate)
	if !ok || got != filepath.Join("nested", "video.mp4") {
		t.Fatalf("RelativeWithin() = %q, %v", got, ok)
	}
}
