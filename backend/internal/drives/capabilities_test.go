package drives

import "testing"

func TestCapabilitiesForKind(t *testing.T) {
	for _, kind := range []string{"quark", "p115", "p123", "pikpak", "wopan", "guangyapan", "onedrive", "googledrive", "webdav"} {
		if !CapabilitiesForKind(kind).Upload {
			t.Fatalf("kind %q should advertise upload", kind)
		}
	}
	for _, kind := range []string{"localstorage", "local-upload", "scriptcrawler", "unknown"} {
		if CapabilitiesForKind(kind).Upload {
			t.Fatalf("kind %q should not advertise upload", kind)
		}
	}
}
