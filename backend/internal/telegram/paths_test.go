package telegram

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAPIPathsMapToLocalStorageAndRejectEscapes(t *testing.T) {
	root := t.TempDir()
	const apiRoot = "/bot-api/data"
	file := filepath.Join(root, "123", "videos", "clip.mp4")
	if err := os.MkdirAll(filepath.Dir(file), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	mapped, err := mapAPIFilePath(apiRoot, root, apiRoot+"/123/videos/clip.mp4")
	if err != nil || mapped != file {
		t.Fatalf("mapped path = %q, error = %v", mapped, err)
	}
	f, _, err := openCachedFile(root, mapped)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	for _, filename := range []string{
		"relative.mp4", apiRoot, apiRoot + "/", "/bot-api/data-other/clip.mp4",
		apiRoot + "/../secret", apiRoot + "/123/../../secret", apiRoot + "/123/../clip.mp4",
		apiRoot + "/123//clip.mp4", apiRoot + "/123\\secret", apiRoot + "/clip\x00.mp4",
	} {
		if _, err := mapAPIFilePath(apiRoot, root, filename); err == nil {
			t.Errorf("accepted %q", filename)
		}
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.mp4")); err != nil {
		t.Skip(err)
	}
	mapped, err = mapAPIFilePath(apiRoot, root, apiRoot+"/linked.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if f, _, err := openCachedFile(root, mapped); err == nil {
		f.Close()
		t.Fatal("mapped symlink escaped local storage")
	}
}
