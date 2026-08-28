package api

import "time"

const (
	activeAssetStatsFreshFor = 3 * time.Second
	idleAssetStatsFreshFor   = 30 * time.Second
)

// assetStatsFreshFor keeps progress numbers close to the existing five-second
// UI polling cadence while avoiding repeated full scans when all work is idle.
// Task state remains an application concern; Catalog only receives a duration.
func (a *AdminServer) assetStatsFreshFor(
	statuses map[string]DriveGenerationStatuses,
) time.Duration {
	nightly := a.nightlyJobStatus()
	if nightly.Running || nightly.Queued {
		return activeAssetStatsFreshFor
	}
	for _, status := range statuses {
		if driveGenerationBusy(status) {
			return activeAssetStatsFreshFor
		}
	}
	return idleAssetStatsFreshFor
}

func (a *AdminServer) invalidateAssetStats() {
	if a != nil && a.Catalog != nil {
		a.Catalog.InvalidateAssetStats()
	}
}
