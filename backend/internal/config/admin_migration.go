package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

var ErrAdminConfigRemoved = errors.New("server.admin is no longer supported; manage administrator accounts in the database")

func validateAdminConfigRemoved(data []byte) error {
	var document struct {
		Server map[string]any `yaml:"server"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		return err
	}
	if _, exists := document.Server["admin"]; exists {
		return ErrAdminConfigRemoved
	}
	return nil
}

// MigrateLegacyAdminCredentials removes plaintext only after the caller has
// durably imported it or confirmed that database accounts already supersede it.
// A failed config write can be retried without overwriting database passwords.
func (m *Manager) MigrateLegacyAdminCredentials(migrate func(username, password string) error) (bool, error) {
	m.updateMu.Lock()
	defer m.updateMu.Unlock()
	data, err := os.ReadFile(m.path)
	if err != nil {
		return false, err
	}
	if err := validateAdminConfigRemoved(data); err == nil {
		return false, nil
	} else if !errors.Is(err, ErrAdminConfigRemoved) {
		return false, err
	}
	var legacy struct {
		Server struct {
			Admin struct {
				Username string `yaml:"username"`
				Password string `yaml:"password"`
			} `yaml:"admin"`
		} `yaml:"server"`
	}
	if err := yaml.Unmarshal(data, &legacy); err != nil {
		return false, err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return false, err
	}
	server, _ := mappingValue(ensureDocumentMapping(&root), "server")
	deleteMappingValue(server, "admin")
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(&root); err != nil {
		_ = encoder.Close()
		return false, err
	}
	if err := encoder.Close(); err != nil {
		return false, err
	}
	updated := out.Bytes()
	if err := validateAdminConfigRemoved(updated); err != nil {
		return false, fmt.Errorf("remove inherited server.admin from config.yaml: %w", err)
	}
	parsed, err := Parse(updated)
	if err != nil {
		return false, err
	}
	if err := migrate(legacy.Server.Admin.Username, legacy.Server.Admin.Password); err != nil {
		return false, err
	}
	if err := writeFileAtomically(m.path, updated, configFileMode(m.path)); err != nil {
		return false, err
	}
	if _, err := m.publishLocked(parsed, configVersion(updated)); err != nil {
		return false, err
	}
	return true, nil
}
