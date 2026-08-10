// Package backuptransfer implements the versioned, source-push protocol used
// to copy verified backup archives between two application servers.
//
// A target administrator creates a short-lived receive token. The source uses
// HTTP or HTTPS to negotiate protocol capabilities, create an import, stream
// a bounded set of large ranges in parallel, and request final verification.
// Range boundaries are sparse durability checkpoints rather than digest units;
// the target performs one authoritative whole-archive SHA-256 before publish.
// HTTPS protects
// the receive token and archive in transit; HTTP exists for explicitly chosen
// IP-and-port deployments where transport encryption is unavailable. The
// target binds the token to the first transfer ID, persists only its hash, and
// publishes the archive through backup.Manager after full-archive validation.
// Durable jobs, committed range checkpoints, and idempotent final receipts allow
// either server to restart without routing backup bytes through a browser.
// Importing an archive never schedules or applies a restore.
package backuptransfer
