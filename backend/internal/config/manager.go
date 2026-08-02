package config

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

const configWatchInterval = time.Second

var ErrVersionConflict = errors.New("config.yaml changed since it was loaded")

// LiveSettings is the subset of config.yaml that the running process can
// safely apply without rebuilding its long-lived dependencies.
type LiveSettings struct {
	NightlyStartTime string `json:"nightlyStartTime"`
}

// LegacyRuntimeSettings carries values written by the short-lived SQLite
// settings implementation. They are consulted only when config.yaml does not
// yet contain the corresponding field.
type LegacyRuntimeSettings struct {
	NightlyStartTime *string
}

type SaveResult struct {
	Version         string       `json:"version"`
	RestartRequired bool         `json:"restartRequired"`
	Settings        LiveSettings `json:"settings"`
}

// Manager owns all config.yaml writes and the in-memory live snapshot. The
// file remains the durable source of truth; the snapshot is only a validated,
// concurrency-safe projection for runtime consumers.
type Manager struct {
	path string

	updateMu sync.Mutex
	mu       sync.RWMutex
	current  *Config
	// observedVersion also records an invalid external revision so the watcher
	// reports it once instead of logging the same rejected bytes every second.
	observedVersion string
	apply           func(LiveSettings)
}

func NewManager(path string) (*Manager, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	parsed, err := Parse(data)
	if err != nil {
		return nil, err
	}
	version := configVersion(data)
	return &Manager{
		path:            path,
		current:         parsed,
		observedVersion: version,
	}, nil
}

func DefaultLiveSettings() LiveSettings {
	return LiveSettings{
		NightlyStartTime: DefaultNightlyStartTime,
	}
}

func liveSettingsFromConfig(cfg *Config) LiveSettings {
	if cfg == nil {
		return DefaultLiveSettings()
	}
	return LiveSettings{
		NightlyStartTime: cfg.Nightly.StartTime,
	}
}

func (m *Manager) LiveSettings() LiveSettings {
	if m == nil {
		return DefaultLiveSettings()
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return liveSettingsFromConfig(m.current)
}

// SetApply installs the callback used to propagate hot-reloadable fields. It
// immediately supplies the current snapshot, which prevents consumers from
// starting with a stale default when wiring order changes.
func (m *Manager) SetApply(apply func(LiveSettings)) {
	if m == nil {
		return
	}
	m.updateMu.Lock()
	defer m.updateMu.Unlock()
	m.mu.Lock()
	m.apply = apply
	settings := liveSettingsFromConfig(m.current)
	m.mu.Unlock()
	if apply != nil {
		apply(settings)
	}
}

// ReadYAML returns the bytes currently on disk and their content version.
func (m *Manager) ReadYAML() ([]byte, string, error) {
	if m == nil {
		return nil, "", errors.New("configuration manager is unavailable")
	}
	data, err := os.ReadFile(m.path)
	if err != nil {
		return nil, "", fmt.Errorf("read config: %w", err)
	}
	return data, configVersion(data), nil
}

// ReplaceYAML validates with the same parser used at startup, rejects a stale
// expected version, atomically replaces the file, then publishes live values.
func (m *Manager) ReplaceYAML(data []byte, expectedVersion string) (SaveResult, error) {
	if m == nil {
		return SaveResult{}, errors.New("configuration manager is unavailable")
	}
	candidate, err := Parse(data)
	if err != nil {
		return SaveResult{}, err
	}

	m.updateMu.Lock()
	defer m.updateMu.Unlock()

	currentData, err := os.ReadFile(m.path)
	if err != nil {
		return SaveResult{}, fmt.Errorf("read current config: %w", err)
	}
	currentVersion := configVersion(currentData)
	if expected := normalizeVersion(expectedVersion); expected != "" && expected != currentVersion {
		return SaveResult{}, ErrVersionConflict
	}
	restartRequired := hasRestartRequiredChange(currentData, data)
	if !bytes.Equal(currentData, data) {
		if err := writeFileAtomically(m.path, data, configFileMode(m.path)); err != nil {
			return SaveResult{}, err
		}
	}
	version := configVersion(data)
	settings := m.publishLocked(candidate, version)
	return SaveResult{
		Version:         version,
		RestartRequired: restartRequired,
		Settings:        settings,
	}, nil
}

// UpdateAdminCredentials routes first-run setup through the same serialized,
// atomic writer as the configuration panel.
func (m *Manager) UpdateAdminCredentials(username, password string) error {
	if m == nil {
		return errors.New("configuration manager is unavailable")
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return errors.New("username is required")
	}
	if password == "" {
		return errors.New("password is required")
	}
	m.updateMu.Lock()
	defer m.updateMu.Unlock()
	data, err := os.ReadFile(m.path)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	updated, err := rewriteAdminCredentials(data, username, password)
	if err != nil {
		return err
	}
	parsed, err := Parse(updated)
	if err != nil {
		return err
	}
	if err := writeFileAtomically(m.path, updated, configFileMode(m.path)); err != nil {
		return err
	}
	m.publishLocked(parsed, configVersion(updated))
	return nil
}

// MigrateLegacyRuntimeSettings performs a one-time schema migration into the
// real YAML document. Existing YAML values always win over SQLite values;
// cron_hour is converted to start_time and then removed to avoid two competing
// fields. The retired duplicate-review switch is removed at the same boundary;
// comments and unrelated unknown nodes are retained by yaml.Node encoding.
func (m *Manager) MigrateLegacyRuntimeSettings(legacy LegacyRuntimeSettings) (bool, error) {
	if m == nil {
		return false, errors.New("configuration manager is unavailable")
	}
	m.updateMu.Lock()
	defer m.updateMu.Unlock()

	data, err := os.ReadFile(m.path)
	if err != nil {
		return false, fmt.Errorf("read config for migration: %w", err)
	}
	parsed, err := Parse(data)
	if err != nil {
		return false, err
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return false, fmt.Errorf("parse config for migration: %w", err)
	}
	document := ensureDocumentMapping(&root)
	nightly := ensureMappingValue(document, "nightly")
	changed := false

	if _, exists := mappingValue(nightly, "start_time"); !exists {
		startTime := parsed.Nightly.StartTime
		if legacy.NightlyStartTime != nil {
			if normalized, normalizeErr := NormalizeNightlyStartTime(*legacy.NightlyStartTime); normalizeErr == nil {
				startTime = normalized
			}
		}
		setScalarValue(nightly, "start_time", startTime)
		changed = true
	}
	if deleteMappingValue(nightly, "cron_hour") {
		changed = true
	}

	if dedupe, exists := mappingValue(document, "dedupe"); exists {
		if deleteMappingValue(dedupe, "duplicate_review_enabled") {
			changed = true
		}
		if len(dedupe.Content) == 0 && deleteMappingValue(document, "dedupe") {
			changed = true
		}
	}

	if !changed {
		m.publishLocked(parsed, configVersion(data))
		return false, nil
	}
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(&root); err != nil {
		_ = encoder.Close()
		return false, fmt.Errorf("encode migrated config: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return false, fmt.Errorf("encode migrated config: %w", err)
	}
	migratedData := out.Bytes()
	migrated, err := Parse(migratedData)
	if err != nil {
		return false, fmt.Errorf("validate migrated config: %w", err)
	}
	if err := writeFileAtomically(m.path, migratedData, configFileMode(m.path)); err != nil {
		return false, err
	}
	m.publishLocked(migrated, configVersion(migratedData))
	return true, nil
}

// Reload applies an externally edited valid file. Invalid intermediate files
// are ignored, keeping the last known-good runtime snapshot in place.
func (m *Manager) Reload() (bool, error) {
	if m == nil {
		return false, errors.New("configuration manager is unavailable")
	}
	m.updateMu.Lock()
	defer m.updateMu.Unlock()
	data, err := os.ReadFile(m.path)
	if err != nil {
		return false, fmt.Errorf("read config: %w", err)
	}
	version := configVersion(data)
	m.mu.RLock()
	observedVersion := m.observedVersion
	m.mu.RUnlock()
	if version == observedVersion {
		return false, nil
	}
	m.mu.Lock()
	m.observedVersion = version
	m.mu.Unlock()
	parsed, err := Parse(data)
	if err != nil {
		return false, err
	}
	m.publishLocked(parsed, version)
	return true, nil
}

func (m *Manager) Watch(ctx context.Context) {
	if m == nil {
		return
	}
	ticker := time.NewTicker(configWatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			changed, err := m.Reload()
			if err != nil {
				log.Printf("[config] external config reload rejected: %v", err)
			} else if changed {
				log.Printf("[config] external config change loaded; live fields applied")
			}
		}
	}
}

// publishLocked requires updateMu. The callback runs after the snapshot lock is
// released but before another publication can overtake it.
func (m *Manager) publishLocked(cfg *Config, version string) LiveSettings {
	m.mu.Lock()
	m.current = cfg
	m.observedVersion = version
	apply := m.apply
	settings := liveSettingsFromConfig(cfg)
	m.mu.Unlock()
	if apply != nil {
		apply(settings)
	}
	return settings
}

func configVersion(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func normalizeVersion(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "W/")
	return strings.Trim(value, `"`)
}

func mappingValue(parent *yaml.Node, key string) (*yaml.Node, bool) {
	if parent == nil || parent.Kind != yaml.MappingNode {
		return nil, false
	}
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			return parent.Content[i+1], true
		}
	}
	return nil, false
}

func deleteMappingValue(parent *yaml.Node, key string) bool {
	if parent == nil || parent.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			parent.Content = append(parent.Content[:i], parent.Content[i+2:]...)
			return true
		}
	}
	return false
}

func hasRestartRequiredChange(before, after []byte) bool {
	var beforeDocument any
	var afterDocument any
	if yaml.Unmarshal(before, &beforeDocument) != nil || yaml.Unmarshal(after, &afterDocument) != nil {
		return true
	}
	removeLiveDocumentValues(beforeDocument)
	removeLiveDocumentValues(afterDocument)
	return !reflect.DeepEqual(beforeDocument, afterDocument)
}

func removeLiveDocumentValues(document any) {
	root, ok := document.(map[string]any)
	if !ok {
		return
	}
	removeNestedValue(root, "nightly", "start_time")
	removeNestedValue(root, "nightly", "cron_hour")
}

func removeNestedValue(root map[string]any, section, key string) {
	value, ok := root[section]
	if !ok {
		return
	}
	mapping, ok := value.(map[string]any)
	if !ok {
		return
	}
	delete(mapping, key)
	if len(mapping) == 0 {
		delete(root, section)
	}
}
