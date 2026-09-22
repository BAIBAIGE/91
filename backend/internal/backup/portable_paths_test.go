package backup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRestoreDoesNotRewritePortableReferencesRelativeToWorkingDirectory(t *testing.T) {
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	moves := []pathRewrite{{from: workingDir, to: t.TempDir()}}
	for _, value := range []string{"video.mp4", "nested/video.mp4", "demo.py"} {
		if got := rewriteRestoredPath(value, moves); got != value {
			t.Fatalf("portable reference %q rewritten to %q", value, got)
		}
	}
	if got := rewriteRestoredPath(filepath.Join(workingDir, "legacy.mp4"), moves); got != filepath.Join(moves[0].to, "legacy.mp4") {
		t.Fatalf("legacy absolute path was not rewritten: %q", got)
	}
}
