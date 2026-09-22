package scriptcrawler

import (
	"path/filepath"
	"testing"
)

func TestImportedScriptIdentityFollowsDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "crawler-scripts")
	credentials := map[string]string{"proxy": "http://localhost:8888"}
	if err := SetScriptPath(credentials, root, filepath.Join(root, "test.py")); err != nil {
		t.Fatal(err)
	}
	if credentials["script_file"] != "test.py" || credentials["script_path"] != "" || !IsConfigured(credentials) {
		t.Fatalf("imported script credentials = %+v", credentials)
	}
	moved := filepath.Join(t.TempDir(), "crawler-scripts")
	got, err := ScriptPath(credentials, moved)
	if err != nil || got != filepath.Join(moved, "test.py") {
		t.Fatalf("moved script = %q, %v", got, err)
	}
	external := filepath.Join(t.TempDir(), "external.py")
	if err := SetScriptPath(credentials, moved, external); err != nil {
		t.Fatal(err)
	}
	if credentials["script_file"] != "" || credentials["script_path"] != external {
		t.Fatalf("external script credentials = %+v", credentials)
	}
	if got, err := ScriptPath(credentials, root); err != nil || got != external {
		t.Fatalf("external script followed data directory: %q, %v", got, err)
	}
}

func TestImportedScriptRejectsAbsoluteAndEscapingReferences(t *testing.T) {
	root := filepath.Join(t.TempDir(), "crawler-scripts")
	for _, file := range []string{"../external.py", ".", filepath.Join(root, "absolute.py")} {
		if path, err := ScriptPath(map[string]string{"script_file": file}, root); err == nil {
			t.Fatalf("accepted script_file %q as %q", file, path)
		}
	}
}
