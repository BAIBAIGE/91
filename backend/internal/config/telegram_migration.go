package config

import (
	"bytes"
	"errors"
	"os"

	"gopkg.in/yaml.v3"
)

// MigrateTelegramSettings imports missing YAML fields from the retired database
// settings. Existing YAML values win, including explicit false or empty values.
// The caller must only delete the legacy row after this durable write succeeds.
func (m *Manager) MigrateTelegramSettings(legacy Telegram) error {
	m.updateMu.Lock()
	defer m.updateMu.Unlock()
	data, err := os.ReadFile(m.path)
	if err != nil {
		return err
	}
	var root yaml.Node
	if err = yaml.Unmarshal(data, &root); err != nil {
		return err
	}
	document := ensureDocumentMapping(&root)
	section, exists := mappingValue(document, "telegram")
	if exists && section.Kind != yaml.MappingNode {
		return errors.New("telegram 必须是映射对象")
	}
	if !exists {
		section = ensureMappingValue(document, "telegram")
	}
	var imported yaml.Node
	if err = imported.Encode(legacy); err != nil {
		return err
	}
	changed := !exists
	for n := 0; n+1 < len(imported.Content); n += 2 {
		if _, exists := mappingValue(section, imported.Content[n].Value); !exists {
			section.Content = append(section.Content, imported.Content[n], imported.Content[n+1])
			changed = true
		}
	}
	if !changed {
		return nil
	}
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err = encoder.Encode(&root); err != nil {
		return err
	}
	if err = encoder.Close(); err != nil {
		return err
	}
	parsed, err := Parse(out.Bytes())
	if err != nil {
		return err
	}
	mode := configFileMode(m.path)
	if err = writeFileAtomically(m.path, out.Bytes(), mode); err != nil {
		return err
	}
	if _, err = m.publishLocked(parsed, configVersion(out.Bytes())); err != nil {
		return errors.Join(err, writeFileAtomically(m.path, data, mode))
	}
	return nil
}
