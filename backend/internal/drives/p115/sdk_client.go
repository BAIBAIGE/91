package p115

import sdk "github.com/SheltonZhu/115driver/pkg/driver"

// newSDKClient gives each SDK operation its own mutable Request field. The
// underlying Resty client is shared; its configuration must remain unchanged
// after Init. Direct Client.R() calls already create independent requests.
// Do not copy the SDK client: its upload auth fields are mutable and belong to
// the upload path, which serializes access through uploadGate.
func (d *Driver) newSDKClient() *sdk.Pan115Client {
	return &sdk.Pan115Client{Client: d.client.Client}
}
