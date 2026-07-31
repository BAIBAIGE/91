package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (m *Manager) List(ctx context.Context) (ListResult, error) {
	estimate, err := m.Estimate(ctx)
	if err != nil {
		return ListResult{}, err
	}
	entries, err := os.ReadDir(m.backupDir)
	if err != nil {
		return ListResult{}, err
	}
	records := make([]BackupRecord, 0)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return ListResult{}, err
		}
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".zip") {
			continue
		}
		if !strings.HasPrefix(entry.Name(), "video-site-91-full-") {
			continue
		}
		archivePath := filepath.Join(m.backupDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		record := BackupRecord{
			ID:                 strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())),
			Name:               entry.Name(),
			Size:               info.Size(),
			CreatedAt:          info.ModTime().UTC(),
			VerificationStatus: "unchecked",
		}
		var meta archiveMeta
		if err := readJSONFile(metaPath(archivePath), &meta); err == nil &&
			meta.Name == entry.Name() &&
			meta.Size == info.Size() &&
			meta.ModifiedAt.Equal(info.ModTime().UTC()) {
			record.SHA256 = meta.SHA256
			record.VerificationStatus = "verified"
			record.Imported = meta.Imported
			applyManifestToRecord(&record, meta.Manifest, info.ModTime())
		} else {
			manifest, inspectErr := InspectArchive(archivePath)
			if inspectErr != nil {
				record.VerificationStatus = "invalid"
				record.VerificationError = inspectErr.Error()
			} else {
				applyManifestToRecord(&record, manifest, info.ModTime())
			}
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].Name > records[j].Name
		}
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})
	_, pendingErr := os.Stat(m.pendingPath)
	return ListResult{
		Backups:         records,
		Current:         m.Current(),
		RestoreProgress: m.restoreProgressSnapshot(),
		Estimate:        estimate,
		RestartManaged:  m.restartManaged,
		PendingRestore:  pendingErr == nil,
	}, nil
}

func applyManifestToRecord(record *BackupRecord, manifest Manifest, fallback time.Time) {
	if record == nil {
		return
	}
	record.CreatedAt = archiveTimestamp(manifest, fallback)
	record.AppVersion = manifest.AppVersion
	record.SourceDataRoot = manifest.SourceDataRoot
	record.FileCount = manifest.FileCount
	record.ExpandedSize = manifest.TotalSize
	record.Included = append([]string(nil), manifest.Included...)
}

func (m *Manager) OpenBackup(id string) (*os.File, os.FileInfo, string, error) {
	archivePath, name, err := m.resolveBackup(id)
	if err != nil {
		return nil, nil, "", err
	}
	file, err := os.Open(archivePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, "", ErrBackupNotFound
		}
		return nil, nil, "", err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, "", err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, nil, "", ErrBackupNotFound
	}
	return file, info, name, nil
}

func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	restoreBusy := m.restoreBusy
	m.mu.Unlock()
	if restoreBusy {
		return ErrRestorePending
	}
	archivePath, _, err := m.resolveBackup(id)
	if err != nil {
		return err
	}
	var marker restoreMarker
	if err := readJSONFile(m.pendingPath, &marker); err == nil && marker.BackupID == id {
		return errors.New("该备份正在等待恢复，不能删除")
	}
	if err := os.Remove(archivePath); err != nil {
		if os.IsNotExist(err) {
			return ErrBackupNotFound
		}
		return err
	}
	_ = os.Remove(metaPath(archivePath))
	return nil
}

func (m *Manager) resolveBackup(id string) (string, string, error) {
	id = strings.TrimSpace(id)
	if id == "" || id == "." || id == ".." || filepath.Base(id) != id ||
		strings.ContainsAny(id, `/\`+"\x00") ||
		!strings.HasPrefix(id, "video-site-91-full-") {
		return "", "", ErrBackupNotFound
	}
	name := id + ".zip"
	archivePath := filepath.Join(m.backupDir, name)
	relative, err := filepath.Rel(m.backupDir, archivePath)
	if err != nil || relative != name {
		return "", "", ErrBackupNotFound
	}
	info, err := os.Lstat(archivePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", ErrBackupNotFound
		}
		return "", "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", "", ErrBackupNotFound
	}
	return archivePath, name, nil
}

func readJSONFile(filePath string, destination any) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	if len(data) > 64<<20 {
		return fmt.Errorf("backup: JSON sidecar is too large")
	}
	return json.Unmarshal(data, destination)
}
