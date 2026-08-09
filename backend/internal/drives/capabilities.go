package drives

import "strings"

// Capabilities describes operations that can be advertised before a concrete
// driver is mounted. Runtime callers must still assert the corresponding
// optional interface (for example Uploader) before doing work.
type Capabilities struct {
	Upload bool
}

// CapabilitiesForKind is the single backend source of truth used by admin APIs
// and validation. It prevents UI and endpoint allowlists from drifting apart.
func CapabilitiesForKind(kind string) Capabilities {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "quark", "p115", "p123", "pikpak", "wopan", "guangyapan", "onedrive", "googledrive", "webdav":
		return Capabilities{Upload: true}
	default:
		return Capabilities{}
	}
}
