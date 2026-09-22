package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Accept an old configuration only when its effective paths already describe
// the fixed layout. Inferring a root from just one of several independent paths
// could open an empty database or lose access to existing media on upgrade.
func (c *Config) importLegacyDataDirectory(data []byte) error {
	var legacy struct {
		Storage struct {
			DBPath          *string `yaml:"db_path"`
			LocalPreviewDir *string `yaml:"local_preview_dir"`
		} `yaml:"storage"`
		Logging struct {
			Directory *string `yaml:"directory"`
		} `yaml:"logging"`
	}
	if err := yaml.Unmarshal(data, &legacy); err != nil {
		return err
	}
	if legacy.Storage.DBPath == nil && legacy.Storage.LocalPreviewDir == nil && legacy.Logging.Directory == nil {
		return nil
	}
	if strings.TrimSpace(c.Storage.DataDir) != "" || strings.TrimSpace(c.Storage.DBDir) != "" {
		return errors.New("storage.data_dir and optional storage.db_dir replace storage.db_path, storage.local_preview_dir and logging.directory; remove the old path settings")
	}
	valueOrDefault := func(value *string, fallback string) string {
		if value == nil || strings.TrimSpace(*value) == "" {
			return fallback
		}
		return strings.TrimSpace(*value)
	}
	dbPath := valueOrDefault(legacy.Storage.DBPath, "./data/video-site.db")
	root := filepath.Dir(dbPath)
	for _, path := range []struct{ value, name string }{
		{dbPath, "video-site.db"},
		{valueOrDefault(legacy.Storage.LocalPreviewDir, "./data/previews"), "previews"},
		{valueOrDefault(legacy.Logging.Directory, "./data/logs"), "logs"},
	} {
		actual, err := filepath.Abs(path.value)
		if err != nil {
			return err
		}
		expected, err := filepath.Abs(filepath.Join(root, path.name))
		if err != nil {
			return err
		}
		if actual != expected {
			return fmt.Errorf("legacy data paths do not share the standard layout: %q must be %q; stop the service, place media, logs and backups under storage.data_dir, then replace the old path settings with storage.data_dir and optional storage.db_dir", path.value, filepath.Join(root, path.name))
		}
	}
	c.Storage.DataDir = root
	return nil
}
