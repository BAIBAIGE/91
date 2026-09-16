// Package backend provides the configuration template bundled with each release.
package backend

import _ "embed"

//go:embed config.example.yaml
var configTemplate string

func ConfigTemplate() []byte {
	return []byte(configTemplate)
}
