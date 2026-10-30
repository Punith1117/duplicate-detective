package quarantine

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// AppDirName is the folder containing application data.
const AppDirName = ".duplicate-detective"

// QuarantineDirName is the subfolder inside .duplicate-detective for quarantined items.
const QuarantineDirName = "quarantine"

// ManifestFileName is the JSON manifest file name.
const ManifestFileName = "manifest.json"

// FilesSubDirName is the directory containing stored quarantined files.
const FilesSubDirName = "files"

// OperationState represents the explicit lifecycle state of a quarantine operation.
type OperationState string

const (
	// StatePending indicates an operation has been registered but the file move is not yet confirmed.
	StatePending OperationState = "pending"

	// StateQuarantined indicates the file has been successfully moved into quarantine.
	StateQuarantined OperationState = "quarantined"

	// StateRestored indicates the file has been restored to its original path.
	StateRestored OperationState = "restored"

	// StateFailed indicates the operation failed or was found in an invalid state.
	StateFailed OperationState = "failed"
)

var (
	ErrNotFound            = errors.New("quarantine: operation not found")
	ErrNotQuarantined      = errors.New("quarantine: operation is not in quarantined state")
	ErrOriginalFileExists  = errors.New("quarantine: refuse to restore: file already exists at original path")
	ErrRevalidationFailed  = errors.New("quarantine: file revalidation failed")
	ErrIntegrityCheck      = errors.New("quarantine: content integrity check failed")
)

// Entry records metadata and status for a single quarantined file.
type Entry struct {
	ID             string         `json:"id"`
	OriginalPath   string         `json:"original_path"`
	QuarantinePath string         `json:"quarantine_path"`
	Size           int64          `json:"size"`
	SHA256         string         `json:"sha256"`
	Timestamp      time.Time      `json:"timestamp"`
	State          OperationState `json:"state"`
	ErrorMessage   string         `json:"error_message,omitempty"`
}

// Manifest represents the persistent JSON manifest tracking all quarantine operations.
type Manifest struct {
	Version     int              `json:"version"`
	LastUpdated time.Time        `json:"last_updated"`
	Entries     map[string]Entry `json:"entries"`
}

// Options configures the Quarantine Manager.
type Options struct {
	// BaseDir is the workspace root where .duplicate-detective is stored.
	BaseDir string

	// QuarantineDir explicitly overrides the quarantine directory path.
	QuarantineDir string
}

// Manager coordinates quarantine and restoration operations while enforcing data safety.
type Manager struct {
	mu            sync.Mutex
	quarantineDir string
	filesDir      string
	manifestPath  string
	manifest      *Manifest
}

// NewManager initializes the quarantine manager, creates necessary directories,
// loads or creates the manifest, and reconciles state at startup.
func NewManager(opts Options) (*Manager, *ReconciliationReport, error) {
	quarantineDir := opts.QuarantineDir
	if quarantineDir == "" {
		base := opts.BaseDir
		if base == "" {
			base = "."
		}
		quarantineDir = filepath.Join(base, AppDirName, QuarantineDirName)
	}

	cleanQuarantineDir := filepath.Clean(quarantineDir)
	filesDir := filepath.Join(cleanQuarantineDir, FilesSubDirName)
	manifestPath := filepath.Join(cleanQuarantineDir, ManifestFileName)

	// Requirement 1: Create .duplicate-detective/quarantine/ and files directory
	if err := os.MkdirAll(filesDir, 0755); err != nil {
		return nil, nil, fmt.Errorf("quarantine: failed to initialize quarantine directory: %w", err)
	}

	m := &Manager{
		quarantineDir: cleanQuarantineDir,
		filesDir:      filesDir,
		manifestPath:  manifestPath,
		manifest: &Manifest{
			Version: 1,
			Entries: make(map[string]Entry),
		},
	}

	// Requirement 9: Reconcile manifest and filesystem state at startup
	report, err := m.startupReconciliation()
	if err != nil {
		return nil, nil, fmt.Errorf("quarantine: reconciliation failed: %w", err)
	}

	return m, report, nil
}

// QuarantineDir returns the root quarantine directory.
func (m *Manager) QuarantineDir() string {
	return m.quarantineDir
}

// ManifestPath returns the absolute path of the JSON manifest.
func (m *Manager) ManifestPath() string {
	return m.manifestPath
}

// GenerateOperationID generates a unique, collision-resistant operation ID.
func GenerateOperationID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("op-%d-%s", time.Now().UnixNano(), hex.EncodeToString(b))
}

// ComputeFileSHA256 calculates the SHA-256 hash of a file.
func ComputeFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// Quarantine moves a file into quarantine after validating its current existence, type, size, and hash.
func (m *Manager) Quarantine(originalPath string, expectedSize int64, expectedHash string) (*Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cleanOriginal := filepath.Clean(originalPath)

	// Requirement 6: Revalidate files before quarantine
	info, err := os.Lstat(cleanOriginal)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot access %q: %v", ErrRevalidationFailed, cleanOriginal, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: %q is a symlink, refusing to quarantine", ErrRevalidationFailed, cleanOriginal)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %q is not a regular file", ErrRevalidationFailed, cleanOriginal)
	}
	if expectedSize > 0 && info.Size() != expectedSize {
		return nil, fmt.Errorf("%w: file size changed for %q (expected %d, got %d)",
			ErrRevalidationFailed, cleanOriginal, expectedSize, info.Size())
	}

	currentHash, err := ComputeFileSHA256(cleanOriginal)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to hash %q: %v", ErrRevalidationFailed, cleanOriginal, err)
	}
	if expectedHash != "" && !strings.EqualFold(currentHash, expectedHash) {
		return nil, fmt.Errorf("%w: file content modified for %q (expected hash %s, got %s)",
			ErrRevalidationFailed, cleanOriginal, expectedHash, currentHash)
	}

	// Requirement 3: Assign unique operation ID
	opID := GenerateOperationID()

	// Unique directory per operation prevents collisions between duplicate filenames
	opSubdir := filepath.Join(m.filesDir, opID)
	quarantinePath := filepath.Join(opSubdir, filepath.Base(cleanOriginal))

	// Requirement 4 & 5: Record original path, quarantine path, size, SHA-256, timestamp, and state: pending
	entry := Entry{
		ID:             opID,
		OriginalPath:   cleanOriginal,
		QuarantinePath: quarantinePath,
		Size:           info.Size(),
		SHA256:         currentHash,
		Timestamp:      time.Now().UTC(),
		State:          StatePending,
	}

	m.manifest.Entries[opID] = entry
	if err := m.saveManifestLocked(); err != nil {
		delete(m.manifest.Entries, opID)
		return nil, fmt.Errorf("quarantine: failed to persist pending manifest: %w", err)
	}

	if err := os.MkdirAll(opSubdir, 0755); err != nil {
		entry.State = StateFailed
		entry.ErrorMessage = fmt.Sprintf("failed to create quarantine subdir: %v", err)
		m.manifest.Entries[opID] = entry
		_ = m.saveManifestLocked()
		return nil, fmt.Errorf("quarantine: cannot create directory %s: %w", opSubdir, err)
	}

	// Perform safe file move
	moveErr := safeMoveFile(cleanOriginal, quarantinePath, currentHash)
	if moveErr != nil {
		entry.State = StateFailed
		entry.ErrorMessage = fmt.Sprintf("file move failed: %v", moveErr)
		entry.Timestamp = time.Now().UTC()
		m.manifest.Entries[opID] = entry
		_ = m.saveManifestLocked()
		return nil, fmt.Errorf("quarantine: move failed: %w", moveErr)
	}

	// Success: Transition to StateQuarantined
	entry.State = StateQuarantined
	entry.Timestamp = time.Now().UTC()
	m.manifest.Entries[opID] = entry
	if err := m.saveManifestLocked(); err != nil {
		// Even if saving manifest fails, the file is safely in quarantine directory
		return &entry, fmt.Errorf("quarantine: file moved but manifest update failed: %w", err)
	}

	return &entry, nil
}

// Restore moves a quarantined file back to its original path.
// It refuses to overwrite any existing file at the original path.
func (m *Manager) Restore(opID string) (*Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, exists := m.manifest.Entries[opID]
	if !exists {
		return nil, fmt.Errorf("%w: ID %s", ErrNotFound, opID)
	}

	if entry.State != StateQuarantined {
		return nil, fmt.Errorf("%w: ID %s has state %q", ErrNotQuarantined, opID, entry.State)
	}

	// Check quarantine file exists and is intact
	qInfo, err := os.Lstat(entry.QuarantinePath)
	if err != nil {
		return nil, fmt.Errorf("quarantine: quarantined file missing at %q: %w", entry.QuarantinePath, err)
	}
	if !qInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("quarantine: quarantined item at %q is not a regular file", entry.QuarantinePath)
	}

	// Verify quarantine file hash matches manifest SHA-256
	qHash, err := ComputeFileSHA256(entry.QuarantinePath)
	if err != nil {
		return nil, fmt.Errorf("quarantine: failed to hash quarantined file: %w", err)
	}
	if !strings.EqualFold(qHash, entry.SHA256) {
		return nil, fmt.Errorf("%w: quarantined file hash %s does not match manifest %s",
			ErrIntegrityCheck, qHash, entry.SHA256)
	}

	// Requirement 7: Refuse to overwrite existing files during restore
	if _, err := os.Lstat(entry.OriginalPath); err == nil {
		return nil, fmt.Errorf("%w: %s", ErrOriginalFileExists, entry.OriginalPath)
	}

	// Ensure destination directory exists
	origDir := filepath.Dir(entry.OriginalPath)
	if err := os.MkdirAll(origDir, 0755); err != nil {
		return nil, fmt.Errorf("quarantine: failed to create destination directory %s: %w", origDir, err)
	}

	// Move file back from quarantine to original path
	if err := safeMoveFile(entry.QuarantinePath, entry.OriginalPath, entry.SHA256); err != nil {
		return nil, fmt.Errorf("quarantine: restore file move failed: %w", err)
	}

	// Clean up empty op subdir inside quarantine
	_ = os.Remove(filepath.Dir(entry.QuarantinePath))

	// Update state to restored
	entry.State = StateRestored
	entry.Timestamp = time.Now().UTC()
	m.manifest.Entries[opID] = entry

	if err := m.saveManifestLocked(); err != nil {
		return &entry, fmt.Errorf("quarantine: file restored but manifest update failed: %w", err)
	}

	return &entry, nil
}

// GetEntry returns a copy of a manifest entry by ID.
func (m *Manager) GetEntry(opID string) (Entry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.manifest.Entries[opID]
	return e, ok
}

// ListEntries returns a snapshot of all entries in the manifest.
func (m *Manager) ListEntries() []Entry {
	m.mu.Lock()
	defer m.mu.Unlock()

	list := make([]Entry, 0, len(m.manifest.Entries))
	for _, e := range m.manifest.Entries {
		list = append(list, e)
	}
	return list
}

// Requirement 8: Use atomic manifest replacement where possible
func (m *Manager) saveManifestLocked() error {
	m.manifest.LastUpdated = time.Now().UTC()
	data, err := json.MarshalIndent(m.manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}

	// Write to temporary file in the same directory to ensure same filesystem for atomic rename
	tmpFile, err := os.CreateTemp(m.quarantineDir, "manifest-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp manifest: %w", err)
	}
	tmpPath := tmpFile.Name()

	writeSuccess := false
	defer func() {
		if !writeSuccess {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(data); err != nil {
		return fmt.Errorf("write temp manifest: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("sync temp manifest: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp manifest: %w", err)
	}

	// Atomic replace
	if err := os.Rename(tmpPath, m.manifestPath); err != nil {
		return fmt.Errorf("atomic rename manifest: %w", err)
	}

	writeSuccess = true
	return nil
}

// safeMoveFile moves a file from src to dst.
// It first attempts os.Rename. If that fails (e.g. cross-device EXDEV),
// it falls back to copying, verifying the SHA-256 hash at dst, and removing src.
func safeMoveFile(src, dst, expectedHash string) error {
	// Attempt fast atomic rename first
	if err := os.Rename(src, dst); err == nil {
		return nil
	}

	// Cross-device or fallback copy
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source file: %w", err)
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return fmt.Errorf("stat source file: %w", err)
	}

	dstDir := filepath.Dir(dst)
	tmpDst, err := os.CreateTemp(dstDir, "move-tmp-*")
	if err != nil {
		return fmt.Errorf("create temp destination: %w", err)
	}
	tmpDstPath := tmpDst.Name()

	copySuccess := false
	defer func() {
		if !copySuccess {
			_ = tmpDst.Close()
			_ = os.Remove(tmpDstPath)
		}
	}()

	if _, err := io.Copy(tmpDst, srcFile); err != nil {
		return fmt.Errorf("copy file data: %w", err)
	}
	if err := tmpDst.Sync(); err != nil {
		return fmt.Errorf("sync file data: %w", err)
	}
	if err := tmpDst.Chmod(srcInfo.Mode()); err != nil {
		// Non-fatal, keep going
	}
	if err := tmpDst.Close(); err != nil {
		return fmt.Errorf("close temp destination: %w", err)
	}

	// Verify integrity before removing source
	if expectedHash != "" {
		hash, err := ComputeFileSHA256(tmpDstPath)
		if err != nil {
			return fmt.Errorf("hash verification on copy failed: %w", err)
		}
		if !strings.EqualFold(hash, expectedHash) {
			return fmt.Errorf("copied file hash mismatch: expected %s, got %s", expectedHash, hash)
		}
	}

	// Rename temp destination to final destination
	if err := os.Rename(tmpDstPath, dst); err != nil {
		return fmt.Errorf("finalize destination file: %w", err)
	}
	copySuccess = true

	// Close source file before unlinking
	_ = srcFile.Close()

	// Remove source file only after destination is fully written, synced, verified, and renamed
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("file copied but failed to remove original: %w", err)
	}

	return nil
}

