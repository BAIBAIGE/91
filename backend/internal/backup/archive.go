package backup

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	maxArchiveFiles           = 1_000_000
	maxManifestBytes    int64 = 64 << 20
	maxExpandedBytes    int64 = 8 << 40
	maxCompressionRatio       = uint64(10_000)
)

type VerifyOptions struct {
	CurrentVersion string
	TempDir        string
	AvailableBytes int64
	Progress       func(OperationProgress)
}

func writeArchive(
	ctx context.Context,
	destination string,
	snapshotRoot string,
	manifest Manifest,
	progress func(bytes int64, fileDone bool),
) error {
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("backup: create archive: %w", err)
	}
	success := false
	defer func() {
		_ = file.Close()
		if !success {
			_ = os.Remove(destination)
		}
	}()
	writer := zip.NewWriter(file)
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		_ = writer.Close()
		return err
	}
	manifestBytes = append(manifestBytes, '\n')
	header := &zip.FileHeader{
		Name:     manifestName,
		Method:   zip.Deflate,
		Modified: manifest.CreatedAt,
	}
	header.SetMode(0o600)
	manifestWriter, err := writer.CreateHeader(header)
	if err != nil {
		_ = writer.Close()
		return err
	}
	if _, err := manifestWriter.Write(manifestBytes); err != nil {
		_ = writer.Close()
		return err
	}

	for _, directory := range []string{
		"payload/previews/",
		"payload/uploads/",
		"payload/crawler-scripts/",
		"payload/scriptcrawlers/",
		"payload/spider91/",
	} {
		dirHeader := &zip.FileHeader{Name: directory, Method: zip.Store, Modified: manifest.CreatedAt}
		dirHeader.SetMode(os.ModeDir | 0o755)
		if _, err := writer.CreateHeader(dirHeader); err != nil {
			_ = writer.Close()
			return err
		}
	}

	for _, entry := range manifest.Files {
		if err := ctx.Err(); err != nil {
			_ = writer.Close()
			return err
		}
		clean, ok := cleanArchivePath(entry.Path)
		if !ok || !strings.HasPrefix(clean, "payload/") {
			_ = writer.Close()
			return fmt.Errorf("backup: invalid snapshot path %q", entry.Path)
		}
		source := filepath.Join(snapshotRoot, filepath.FromSlash(clean))
		input, err := os.Open(source)
		if err != nil {
			_ = writer.Close()
			return err
		}
		entryHeader := &zip.FileHeader{
			Name:     clean,
			Method:   compressionMethod(clean),
			Modified: manifest.CreatedAt,
		}
		mode := os.FileMode(entry.Mode)
		if mode == 0 {
			mode = 0o600
		}
		entryHeader.SetMode(mode.Perm())
		output, err := writer.CreateHeader(entryHeader)
		if err != nil {
			_ = input.Close()
			_ = writer.Close()
			return err
		}
		written, err := copyWithContext(ctx, output, input, func(n int64) {
			if progress != nil {
				progress(n, false)
			}
		})
		closeErr := input.Close()
		if err != nil {
			_ = writer.Close()
			return err
		}
		if closeErr != nil {
			_ = writer.Close()
			return closeErr
		}
		if written != entry.Size {
			_ = writer.Close()
			return fmt.Errorf("backup: snapshot file %s changed while archiving", clean)
		}
		if progress != nil {
			progress(0, true)
		}
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("backup: finalize ZIP: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("backup: sync ZIP: %w", err)
	}
	if err := file.Close(); err != nil {
		return err
	}
	success = true
	return nil
}

func compressionMethod(name string) uint16 {
	switch strings.ToLower(path.Ext(name)) {
	case ".mp4", ".mkv", ".mov", ".avi", ".webm", ".jpg", ".jpeg", ".png",
		".webp", ".gif", ".zip", ".gz", ".7z", ".rar":
		return zip.Store
	default:
		return zip.Deflate
	}
}

func copyWithContext(ctx context.Context, destination io.Writer, source io.Reader, onWrite func(int64)) (int64, error) {
	buffer := make([]byte, 1<<20)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			written, writeErr := destination.Write(buffer[:read])
			total += int64(written)
			if onWrite != nil && written > 0 {
				onWrite(int64(written))
			}
			if writeErr != nil {
				return total, writeErr
			}
			if written != read {
				return total, io.ErrShortWrite
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return total, nil
			}
			return total, readErr
		}
	}
}

func InspectArchive(archivePath string) (Manifest, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return Manifest{}, fmt.Errorf("backup: open ZIP: %w", err)
	}
	defer reader.Close()
	manifest, _, err := inspectZIP(&reader.Reader)
	return manifest, err
}

func VerifyArchive(ctx context.Context, archivePath string, options VerifyOptions) (ValidationReport, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return ValidationReport{}, fmt.Errorf("backup: open ZIP: %w", err)
	}
	defer reader.Close()
	manifest, filesByName, expected, names, err := validateArchiveMetadata(&reader.Reader, options)
	if err != nil {
		return ValidationReport{}, err
	}

	tempDir := strings.TrimSpace(options.TempDir)
	if tempDir == "" {
		tempDir = os.TempDir()
	}
	if err := os.MkdirAll(tempDir, 0o700); err != nil {
		return ValidationReport{}, err
	}
	databaseFile, err := os.CreateTemp(tempDir, "backup-verify-*.sqlite")
	if err != nil {
		return ValidationReport{}, err
	}
	databasePath := databaseFile.Name()
	defer os.Remove(databasePath)
	databaseWritten := false
	progress := OperationProgress{
		Phase:      "files",
		TotalBytes: manifest.TotalSize,
		TotalFiles: manifest.FileCount,
	}
	emitArchiveProgress(options, progress)

	for _, name := range names {
		if err := ctx.Err(); err != nil {
			_ = databaseFile.Close()
			return ValidationReport{}, err
		}
		var output io.Writer
		if name == "payload/database.sqlite" {
			output = databaseFile
			databaseWritten = true
		}
		if err := verifyArchiveEntry(
			ctx,
			name,
			filesByName[name],
			expected[name],
			output,
			func(bytes int64) {
				progress.ProcessedBytes += bytes
				emitArchiveProgress(options, progress)
			},
		); err != nil {
			_ = databaseFile.Close()
			return ValidationReport{}, err
		}
		progress.ProcessedFiles++
		emitArchiveProgress(options, progress)
	}
	if err := databaseFile.Sync(); err != nil {
		_ = databaseFile.Close()
		return ValidationReport{}, err
	}
	if err := databaseFile.Close(); err != nil {
		return ValidationReport{}, err
	}
	if !databaseWritten {
		return ValidationReport{}, errors.New("backup: database entry was not verified")
	}
	progress.Phase = "database"
	emitArchiveProgress(options, progress)
	if err := verifySQLite(databasePath); err != nil {
		return ValidationReport{}, err
	}

	return archiveValidationReport(manifest, options), nil
}

// VerifyAndExtractArchive validates the archive while writing the exact bytes
// being validated into an isolated staging directory. Restore callers use this
// instead of validating and then decompressing the same archive a second time.
func VerifyAndExtractArchive(
	ctx context.Context,
	archivePath string,
	stageRoot string,
	options VerifyOptions,
) (ValidationReport, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return ValidationReport{}, fmt.Errorf("backup: open ZIP: %w", err)
	}
	defer reader.Close()
	manifest, filesByName, expected, names, err := validateArchiveMetadata(&reader.Reader, options)
	if err != nil {
		return ValidationReport{}, err
	}
	progress := OperationProgress{
		Phase:      "files",
		TotalBytes: manifest.TotalSize,
		TotalFiles: manifest.FileCount,
	}
	emitArchiveProgress(options, progress)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return ValidationReport{}, err
		}
		target := filepath.Join(stageRoot, filepath.FromSlash(name))
		relative, err := filepath.Rel(stageRoot, target)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return ValidationReport{}, fmt.Errorf("backup: restore path escapes staging directory: %q", name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return ValidationReport{}, err
		}
		mode := os.FileMode(expected[name].Mode).Perm()
		if mode == 0 {
			mode = 0o600
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return ValidationReport{}, err
		}
		verifyErr := verifyArchiveEntry(
			ctx,
			name,
			filesByName[name],
			expected[name],
			output,
			func(bytes int64) {
				progress.ProcessedBytes += bytes
				emitArchiveProgress(options, progress)
			},
		)
		syncErr := output.Sync()
		closeErr := output.Close()
		if verifyErr != nil || syncErr != nil || closeErr != nil {
			_ = os.Remove(target)
			for _, candidate := range []error{verifyErr, syncErr, closeErr} {
				if candidate != nil {
					return ValidationReport{}, candidate
				}
			}
		}
		progress.ProcessedFiles++
		emitArchiveProgress(options, progress)
	}
	progress.Phase = "database"
	emitArchiveProgress(options, progress)
	if err := verifySQLite(filepath.Join(stageRoot, "payload", "database.sqlite")); err != nil {
		return ValidationReport{}, err
	}
	return archiveValidationReport(manifest, options), nil
}

func validateArchiveMetadata(
	reader *zip.Reader,
	options VerifyOptions,
) (Manifest, map[string]*zip.File, map[string]ManifestFile, []string, error) {
	manifest, filesByName, err := inspectZIP(reader)
	if err != nil {
		return Manifest{}, nil, nil, nil, err
	}
	if manifest.FormatVersion != FormatVersion {
		return Manifest{}, nil, nil, nil, fmt.Errorf("backup: unsupported format version %d", manifest.FormatVersion)
	}
	if newerVersion(manifest.AppVersion, options.CurrentVersion) {
		return Manifest{}, nil, nil, nil, fmt.Errorf(
			"backup: source version %s is newer than this application (%s)",
			manifest.AppVersion,
			normalizedVersion(options.CurrentVersion),
		)
	}
	if options.AvailableBytes > 0 &&
		manifest.TotalSize > options.AvailableBytes-diskSafetyReserve {
		return Manifest{}, nil, nil, nil, fmt.Errorf(
			"%w：展开需要 %d 字节，可用 %d 字节",
			ErrInsufficientSpace,
			manifest.TotalSize,
			options.AvailableBytes,
		)
	}

	expected := make(map[string]ManifestFile, len(manifest.Files))
	var declaredTotal int64
	databaseDeclared := false
	configDeclared := false
	for _, entry := range manifest.Files {
		clean, ok := cleanArchivePath(entry.Path)
		if !ok || !validPayloadPath(clean) {
			return Manifest{}, nil, nil, nil, fmt.Errorf("backup: manifest contains invalid path %q", entry.Path)
		}
		if _, exists := expected[clean]; exists {
			return Manifest{}, nil, nil, nil, fmt.Errorf("backup: manifest contains duplicate path %q", clean)
		}
		if entry.Size < 0 || !validSHA256(entry.SHA256) {
			return Manifest{}, nil, nil, nil, fmt.Errorf("backup: invalid manifest metadata for %q", clean)
		}
		expected[clean] = entry
		if declaredTotal > math.MaxInt64-entry.Size {
			return Manifest{}, nil, nil, nil, errors.New("backup: expanded size overflow")
		}
		declaredTotal += entry.Size
		databaseDeclared = databaseDeclared || clean == "payload/database.sqlite"
		configDeclared = configDeclared || clean == "payload/config.yaml"
	}
	if !databaseDeclared || !configDeclared {
		return Manifest{}, nil, nil, nil, errors.New("backup: database or configuration is missing")
	}
	if manifest.FileCount != len(manifest.Files) || manifest.FileCount != len(expected) {
		return Manifest{}, nil, nil, nil, errors.New("backup: manifest file count does not match")
	}
	if manifest.TotalSize != declaredTotal {
		return Manifest{}, nil, nil, nil, errors.New("backup: manifest expanded size does not match")
	}

	names := make([]string, 0, len(expected))
	for name := range expected {
		names = append(names, name)
	}
	sort.Strings(names)
	return manifest, filesByName, expected, names, nil
}

func verifyArchiveEntry(
	ctx context.Context,
	name string,
	zipFile *zip.File,
	expected ManifestFile,
	destination io.Writer,
	onWrite func(int64),
) error {
	if zipFile == nil {
		return fmt.Errorf("backup: ZIP entry %q is missing", name)
	}
	if zipFile.UncompressedSize64 != uint64(expected.Size) {
		return fmt.Errorf("backup: size mismatch for %q", name)
	}
	input, err := zipFile.Open()
	if err != nil {
		return err
	}
	hash := sha256.New()
	output := io.Writer(hash)
	if destination != nil {
		output = io.MultiWriter(hash, destination)
	}
	written, copyErr := copyWithContext(ctx, output, input, onWrite)
	closeErr := input.Close()
	if copyErr != nil {
		return fmt.Errorf("backup: read %q: %w", name, copyErr)
	}
	if closeErr != nil {
		return closeErr
	}
	if written != expected.Size {
		return fmt.Errorf("backup: truncated entry %q", name)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, expected.SHA256) {
		return fmt.Errorf("backup: SHA-256 mismatch for %q", name)
	}
	return nil
}

func emitArchiveProgress(options VerifyOptions, progress OperationProgress) {
	if options.Progress != nil {
		options.Progress(normalizeOperationProgress(progress))
	}
}

func archiveValidationReport(manifest Manifest, options VerifyOptions) ValidationReport {
	report := ValidationReport{
		Manifest:           manifest,
		VerificationStatus: "verified",
	}
	if normalizedVersion(options.CurrentVersion) == "unknown" {
		report.Warnings = append(report.Warnings, "当前应用版本未知，未执行应用版本新旧比较")
	}
	return report
}

func inspectZIP(reader *zip.Reader) (Manifest, map[string]*zip.File, error) {
	if len(reader.File) == 0 || len(reader.File) > maxArchiveFiles+16 {
		return Manifest{}, nil, errors.New("backup: ZIP file count is invalid")
	}
	filesByName := make(map[string]*zip.File, len(reader.File))
	caseNames := make(map[string]string, len(reader.File))
	var manifestFile *zip.File
	var expanded uint64
	for _, file := range reader.File {
		cleanName := strings.TrimSuffix(file.Name, "/")
		clean, ok := cleanArchivePath(cleanName)
		if !ok {
			return Manifest{}, nil, fmt.Errorf("backup: unsafe ZIP path %q", file.Name)
		}
		lower := strings.ToLower(clean)
		if prior, exists := caseNames[lower]; exists {
			return Manifest{}, nil, fmt.Errorf("backup: duplicate ZIP entries %q and %q", prior, clean)
		}
		caseNames[lower] = clean
		if file.FileInfo().IsDir() {
			if cleanName == "" {
				return Manifest{}, nil, errors.New("backup: invalid directory entry")
			}
			if !validPayloadDirectory(clean) {
				return Manifest{}, nil, fmt.Errorf("backup: invalid directory path %q", file.Name)
			}
			continue
		}
		if file.Flags&0x1 != 0 {
			return Manifest{}, nil, fmt.Errorf("backup: encrypted ZIP entry %q is not supported", clean)
		}
		mode := file.Mode()
		if mode&os.ModeSymlink != 0 || (!mode.IsRegular() && mode.Type() != 0) {
			return Manifest{}, nil, fmt.Errorf("backup: non-regular ZIP entry %q is not allowed", clean)
		}
		if file.UncompressedSize64 > uint64(maxExpandedBytes) ||
			expanded > uint64(maxExpandedBytes)-file.UncompressedSize64 {
			return Manifest{}, nil, errors.New("backup: expanded archive is too large")
		}
		expanded += file.UncompressedSize64
		if file.UncompressedSize64 > 1<<20 {
			compressed := file.CompressedSize64
			if compressed == 0 || file.UncompressedSize64/compressed > maxCompressionRatio {
				return Manifest{}, nil, fmt.Errorf("backup: suspicious compression ratio for %q", clean)
			}
		}
		if clean == manifestName {
			manifestFile = file
		} else {
			if !validPayloadPath(clean) {
				return Manifest{}, nil, fmt.Errorf("backup: unsupported payload path %q", clean)
			}
			filesByName[clean] = file
		}
	}
	if manifestFile == nil {
		return Manifest{}, nil, errors.New("backup: manifest.json is missing")
	}
	if manifestFile.UncompressedSize64 == 0 || manifestFile.UncompressedSize64 > uint64(maxManifestBytes) {
		return Manifest{}, nil, errors.New("backup: manifest size is invalid")
	}
	input, err := manifestFile.Open()
	if err != nil {
		return Manifest{}, nil, err
	}
	defer input.Close()
	var manifest Manifest
	decoder := json.NewDecoder(io.LimitReader(input, maxManifestBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, nil, fmt.Errorf("backup: decode manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Manifest{}, nil, errors.New("backup: manifest contains trailing data")
	}
	if manifest.FormatVersion <= 0 || manifest.FileCount < 0 || manifest.FileCount > maxArchiveFiles ||
		manifest.TotalSize < 0 || manifest.TotalSize > maxExpandedBytes {
		return Manifest{}, nil, errors.New("backup: manifest limits are invalid")
	}
	if len(manifest.Included) > 32 {
		return Manifest{}, nil, errors.New("backup: manifest included-directory list is invalid")
	}
	if len(filesByName) != manifest.FileCount {
		return Manifest{}, nil, errors.New("backup: ZIP and manifest file counts differ")
	}
	return manifest, filesByName, nil
}

func validPayloadDirectory(name string) bool {
	switch name {
	case "payload", "payload/previews", "payload/uploads", "payload/crawler-scripts",
		"payload/scriptcrawlers", "payload/spider91":
		return true
	default:
		return strings.HasPrefix(name, "payload/previews/") ||
			strings.HasPrefix(name, "payload/uploads/") ||
			strings.HasPrefix(name, "payload/crawler-scripts/") ||
			strings.HasPrefix(name, "payload/scriptcrawlers/") ||
			strings.HasPrefix(name, "payload/spider91/")
	}
}

func validPayloadPath(name string) bool {
	if name == "payload/database.sqlite" || name == "payload/config.yaml" {
		return true
	}
	return strings.HasPrefix(name, "payload/previews/") ||
		strings.HasPrefix(name, "payload/uploads/") ||
		strings.HasPrefix(name, "payload/crawler-scripts/") ||
		strings.HasPrefix(name, "payload/scriptcrawlers/") ||
		strings.HasPrefix(name, "payload/spider91/")
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func verifySQLite(databasePath string) error {
	dsn := (&url.URL{
		Scheme:   "file",
		Path:     filepath.ToSlash(databasePath),
		RawQuery: "mode=ro&_pragma=busy_timeout(5000)",
	}).String()
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("backup: open SQLite snapshot: %w", err)
	}
	defer database.Close()
	var integrity string
	if err := database.QueryRow(`PRAGMA quick_check`).Scan(&integrity); err != nil {
		return fmt.Errorf("backup: SQLite integrity check: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(integrity), "ok") {
		return fmt.Errorf("backup: SQLite integrity check failed: %s", integrity)
	}
	requiredTables := []string{
		"videos", "drives", "users", "admin_sessions", "settings",
		"remote_upload_jobs", "video_shares", "deleted_videos",
	}
	for _, table := range requiredTables {
		var count int
		if err := database.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`,
			table,
		).Scan(&count); err != nil {
			return fmt.Errorf("backup: inspect required table %s: %w", table, err)
		}
		if count != 1 {
			return fmt.Errorf("backup: required table %s is missing", table)
		}
	}
	return nil
}

func newerVersion(source, current string) bool {
	sourceParts, sourceOK := parseVersion(source)
	currentParts, currentOK := parseVersion(current)
	if !sourceOK || !currentOK {
		return false
	}
	for index := 0; index < 3; index++ {
		if sourceParts[index] == currentParts[index] {
			continue
		}
		return sourceParts[index] > currentParts[index]
	}
	return false
}

func parseVersion(value string) ([3]int, bool) {
	var result [3]int
	value = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(value), "v"))
	if value == "" || value == "unknown" {
		return result, false
	}
	if index := strings.IndexAny(value, "-+"); index >= 0 {
		value = value[:index]
	}
	parts := strings.Split(value, ".")
	if len(parts) < 1 || len(parts) > 3 {
		return result, false
	}
	for index, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return result, false
		}
		result[index] = number
	}
	return result, true
}

func archiveTimestamp(manifest Manifest, fallback time.Time) time.Time {
	if !manifest.CreatedAt.IsZero() {
		return manifest.CreatedAt
	}
	return fallback.UTC()
}
