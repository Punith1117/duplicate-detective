package quarantine

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// helper to create a test file with given content
func createTestFile(t *testing.T, dir, relPath, content string) string {
	t.Helper()
	fullPath := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		t.Fatalf("failed to create dir for %s: %v", fullPath, err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write file %s: %v", fullPath, err)
	}
	return fullPath
}

func TestQuarantine_DirectoryAndManifestCreation(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, report, err := NewManager(Options{BaseDir: tmpDir})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	if report == nil {
		t.Fatal("expected non-nil reconciliation report")
	}

	// Requirement 1: Verify .duplicate-detective/quarantine/ directory is created
	expectedQuarantineDir := filepath.Join(tmpDir, AppDirName, QuarantineDirName)
	if _, err := os.Stat(expectedQuarantineDir); err != nil {
		t.Fatalf("quarantine directory does not exist: %v", err)
	}

	// Create a test file to quarantine
	filePath := createTestFile(t, tmpDir, "files/doc.txt", "hello duplicate world")
	hash, err := ComputeFileSHA256(filePath)
	if err != nil {
		t.Fatalf("failed to compute hash: %v", err)
	}
	info, _ := os.Stat(filePath)

	// Quarantine the file
	entry, err := mgr.Quarantine(filePath, info.Size(), hash)
	if err != nil {
		t.Fatalf("Quarantine failed: %v", err)
	}

	// Requirement 3: Unique operation ID
	if entry.ID == "" || !strings.HasPrefix(entry.ID, "op-") {
		t.Errorf("expected valid operation ID, got %q", entry.ID)
	}

	// Requirement 4 & 5: Record original path, quarantine path, size, SHA-256, timestamp, and state
	if entry.OriginalPath != filePath {
		t.Errorf("original path mismatch: expected %q, got %q", filePath, entry.OriginalPath)
	}
	if entry.Size != info.Size() {
		t.Errorf("size mismatch: expected %d, got %d", info.Size(), entry.Size)
	}
	if entry.SHA256 != hash {
		t.Errorf("hash mismatch: expected %q, got %q", hash, entry.SHA256)
	}
	if entry.State != StateQuarantined {
		t.Errorf("expected state quarantined, got %q", entry.State)
	}
	if entry.Timestamp.IsZero() {
		t.Errorf("timestamp should not be zero")
	}

	// Check original file was moved
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Errorf("original file still exists at original path: %v", err)
	}

	// Check quarantined file exists at quarantine path
	if _, err := os.Stat(entry.QuarantinePath); err != nil {
		t.Fatalf("file not found at quarantine path %s: %v", entry.QuarantinePath, err)
	}

	// Requirement 2: Persist JSON manifest
	manifestData, err := os.ReadFile(mgr.ManifestPath())
	if err != nil {
		t.Fatalf("failed to read manifest file: %v", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}

	savedEntry, ok := manifest.Entries[entry.ID]
	if !ok {
		t.Fatalf("entry %s not found in persisted manifest", entry.ID)
	}
	if savedEntry.State != StateQuarantined {
		t.Errorf("persisted manifest entry state is %s, expected quarantined", savedEntry.State)
	}
}

func TestQuarantine_RevalidationBeforeQuarantine(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, _, err := NewManager(Options{BaseDir: tmpDir})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	filePath := createTestFile(t, tmpDir, "data/test.txt", "data to validate")
	validHash, _ := ComputeFileSHA256(filePath)
	info, _ := os.Stat(filePath)

	// Case 1: Non-existent file fails revalidation
	_, err = mgr.Quarantine(filepath.Join(tmpDir, "does-not-exist.txt"), 100, validHash)
	if err == nil || !errors.Is(err, ErrRevalidationFailed) {
		t.Fatalf("expected ErrRevalidationFailed for missing file, got %v", err)
	}

	// Case 2: Size mismatch fails revalidation
	_, err = mgr.Quarantine(filePath, info.Size()+50, validHash)
	if err == nil || !errors.Is(err, ErrRevalidationFailed) {
		t.Fatalf("expected ErrRevalidationFailed for size mismatch, got %v", err)
	}
	// Verify file was NOT moved
	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("file should still exist after failed revalidation: %v", err)
	}

	// Case 3: Hash mismatch fails revalidation
	_, err = mgr.Quarantine(filePath, info.Size(), "wronghash1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	if err == nil || !errors.Is(err, ErrRevalidationFailed) {
		t.Fatalf("expected ErrRevalidationFailed for hash mismatch, got %v", err)
	}
	// Verify file was NOT moved
	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("file should still exist after failed revalidation: %v", err)
	}

	// Case 4: Directory path fails revalidation
	dirPath := filepath.Join(tmpDir, "data")
	_, err = mgr.Quarantine(dirPath, 0, "")
	if err == nil || !errors.Is(err, ErrRevalidationFailed) {
		t.Fatalf("expected ErrRevalidationFailed for directory, got %v", err)
	}
}

func TestRestore_Success(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, _, err := NewManager(Options{BaseDir: tmpDir})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	content := "valuable user data that was quarantined by mistake"
	filePath := createTestFile(t, tmpDir, "sub/folder/file.txt", content)
	hash, _ := ComputeFileSHA256(filePath)
	info, _ := os.Stat(filePath)

	// Quarantine
	entry, err := mgr.Quarantine(filePath, info.Size(), hash)
	if err != nil {
		t.Fatalf("Quarantine failed: %v", err)
	}

	// Restore
	restoredEntry, err := mgr.Restore(entry.ID)
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
	if restoredEntry.State != StateRestored {
		t.Errorf("expected restored state, got %s", restoredEntry.State)
	}

	// Verify original file exists with exact content
	restoredContent, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read restored file at %s: %v", filePath, err)
	}
	if string(restoredContent) != content {
		t.Fatalf("content mismatch after restore: expected %q, got %q", content, string(restoredContent))
	}

	// Verify quarantine path is gone
	if _, err := os.Stat(entry.QuarantinePath); !os.IsNotExist(err) {
		t.Fatalf("quarantined file should have been moved out of quarantine directory: %v", err)
	}

	// Verify manifest reflects StateRestored
	savedEntry, ok := mgr.GetEntry(entry.ID)
	if !ok {
		t.Fatal("entry missing from manifest")
	}
	if savedEntry.State != StateRestored {
		t.Errorf("expected manifest entry to be restored, got %s", savedEntry.State)
	}
}

func TestRestoreConflict_RefuseToOverwrite(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, _, err := NewManager(Options{BaseDir: tmpDir})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	originalContent := "original file content"
	filePath := createTestFile(t, tmpDir, "safe/doc.txt", originalContent)
	hash, _ := ComputeFileSHA256(filePath)
	info, _ := os.Stat(filePath)

	entry, err := mgr.Quarantine(filePath, info.Size(), hash)
	if err != nil {
		t.Fatalf("Quarantine failed: %v", err)
	}

	// Requirement 7: Simulate conflict: a new file now exists at the original path
	newConflictingContent := "new conflicting file created at original location"
	createTestFile(t, tmpDir, "safe/doc.txt", newConflictingContent)

	// Attempt restore: MUST REFUSE to overwrite
	_, err = mgr.Restore(entry.ID)
	if err == nil {
		t.Fatal("expected restore to fail due to conflict, but it succeeded")
	}
	if !errors.Is(err, ErrOriginalFileExists) {
		t.Fatalf("expected ErrOriginalFileExists, got %v", err)
	}

	// Verify existing file was not overwritten
	currentContent, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read existing file: %v", err)
	}
	if string(currentContent) != newConflictingContent {
		t.Fatalf("existing file was modified! Expected %q, got %q", newConflictingContent, string(currentContent))
	}

	// Verify quarantine file is intact
	qContent, err := os.ReadFile(entry.QuarantinePath)
	if err != nil {
		t.Fatalf("quarantine file missing: %v", err)
	}
	if string(qContent) != originalContent {
		t.Fatalf("quarantine file corrupted! Expected %q, got %q", originalContent, string(qContent))
	}

	// Verify state remains quarantined
	savedEntry, _ := mgr.GetEntry(entry.ID)
	if savedEntry.State != StateQuarantined {
		t.Fatalf("entry state should remain quarantined after restore conflict, got %s", savedEntry.State)
	}
}

func TestRestartRecovery_InterruptedOperation(t *testing.T) {
	// Case 1: Interrupted pending operation where file was already moved to quarantine
	t.Run("FileMovedBeforeCrash", func(t *testing.T) {
		tmpDir := t.TempDir()
		quarantineDir := filepath.Join(tmpDir, AppDirName, QuarantineDirName)
		filesDir := filepath.Join(quarantineDir, FilesSubDirName)
		manifestPath := filepath.Join(quarantineDir, ManifestFileName)

		opID := "op-crash-1"
		content := "crash test content 1"
		opSubdir := filepath.Join(filesDir, opID)
		_ = os.MkdirAll(opSubdir, 0755)
		qFilePath := filepath.Join(opSubdir, "data.txt")
		_ = os.WriteFile(qFilePath, []byte(content), 0644)
		hash, _ := ComputeFileSHA256(qFilePath)

		origFilePath := filepath.Join(tmpDir, "work", "data.txt")

		// Craft manifest in pending state
		crashManifest := Manifest{
			Version: 1,
			Entries: map[string]Entry{
				opID: {
					ID:             opID,
					OriginalPath:   origFilePath,
					QuarantinePath: qFilePath,
					Size:           int64(len(content)),
					SHA256:         hash,
					Timestamp:      time.Now().UTC(),
					State:          StatePending,
				},
			},
		}
		data, _ := json.MarshalIndent(crashManifest, "", "  ")
		_ = os.WriteFile(manifestPath, data, 0644)

		// Initialize manager -> triggers startup reconciliation
		mgr, report, err := NewManager(Options{BaseDir: tmpDir})
		if err != nil {
			t.Fatalf("manager startup failed: %v", err)
		}

		if report.RecoveredEntries != 1 {
			t.Errorf("expected 1 recovered entry, got %d", report.RecoveredEntries)
		}

		entry, ok := mgr.GetEntry(opID)
		if !ok {
			t.Fatal("entry missing after restart")
		}
		if entry.State != StateQuarantined {
			t.Fatalf("expected state quarantined after recovery, got %s", entry.State)
		}
	})

	// Case 2: Interrupted pending operation where file was NOT moved before crash
	t.Run("FileNotMovedBeforeCrash", func(t *testing.T) {
		tmpDir := t.TempDir()
		quarantineDir := filepath.Join(tmpDir, AppDirName, QuarantineDirName)
		filesDir := filepath.Join(quarantineDir, FilesSubDirName)
		manifestPath := filepath.Join(quarantineDir, ManifestFileName)
		_ = os.MkdirAll(filesDir, 0755)

		opID := "op-crash-2"
		content := "file preserved at origin"
		origFilePath := createTestFile(t, tmpDir, "work/preserve.txt", content)
		hash, _ := ComputeFileSHA256(origFilePath)
		qFilePath := filepath.Join(filesDir, opID, "preserve.txt")

		crashManifest := Manifest{
			Version: 1,
			Entries: map[string]Entry{
				opID: {
					ID:             opID,
					OriginalPath:   origFilePath,
					QuarantinePath: qFilePath,
					Size:           int64(len(content)),
					SHA256:         hash,
					Timestamp:      time.Now().UTC(),
					State:          StatePending,
				},
			},
		}
		data, _ := json.MarshalIndent(crashManifest, "", "  ")
		_ = os.WriteFile(manifestPath, data, 0644)

		mgr, report, err := NewManager(Options{BaseDir: tmpDir})
		if err != nil {
			t.Fatalf("manager startup failed: %v", err)
		}

		if report.FailedEntries != 1 {
			t.Errorf("expected 1 failed entry, got %d", report.FailedEntries)
		}

		entry, ok := mgr.GetEntry(opID)
		if !ok {
			t.Fatal("entry missing after restart")
		}
		if entry.State != StateFailed {
			t.Fatalf("expected state failed, got %s", entry.State)
		}
		// Verify original file is still intact
		if _, err := os.Stat(origFilePath); err != nil {
			t.Fatalf("original file must remain untouched: %v", err)
		}
	})

	// Case 3: Interrupted pending operation where file exists at BOTH paths
	t.Run("AmbiguousFileAtBothPaths", func(t *testing.T) {
		tmpDir := t.TempDir()
		quarantineDir := filepath.Join(tmpDir, AppDirName, QuarantineDirName)
		filesDir := filepath.Join(quarantineDir, FilesSubDirName)
		manifestPath := filepath.Join(quarantineDir, ManifestFileName)

		opID := "op-crash-3"
		origFilePath := createTestFile(t, tmpDir, "work/dup.txt", "content at origin")
		opSubdir := filepath.Join(filesDir, opID)
		_ = os.MkdirAll(opSubdir, 0755)
		qFilePath := filepath.Join(opSubdir, "dup.txt")
		_ = os.WriteFile(qFilePath, []byte("content in quarantine"), 0644)

		crashManifest := Manifest{
			Version: 1,
			Entries: map[string]Entry{
				opID: {
					ID:             opID,
					OriginalPath:   origFilePath,
					QuarantinePath: qFilePath,
					Size:           17,
					SHA256:         "dummyhash",
					Timestamp:      time.Now().UTC(),
					State:          StatePending,
				},
			},
		}
		data, _ := json.MarshalIndent(crashManifest, "", "  ")
		_ = os.WriteFile(manifestPath, data, 0644)

		mgr, report, err := NewManager(Options{BaseDir: tmpDir})
		if err != nil {
			t.Fatalf("manager startup failed: %v", err)
		}

		// Requirement 11: Preserve ambiguous files and report recovery warnings
		if report.FailedEntries != 1 {
			t.Errorf("expected 1 failed/ambiguous entry, got %d", report.FailedEntries)
		}
		entry, _ := mgr.GetEntry(opID)
		if entry.State != StateFailed {
			t.Errorf("expected state failed for ambiguous files, got %s", entry.State)
		}
		// Both files MUST still exist on disk!
		if _, err := os.Stat(origFilePath); err != nil {
			t.Fatalf("original file must be preserved: %v", err)
		}
		if _, err := os.Stat(qFilePath); err != nil {
			t.Fatalf("quarantine file must be preserved: %v", err)
		}
	})
}

func TestStaleManifestRecovery_UntrackedAndAlteredFiles(t *testing.T) {
	tmpDir := t.TempDir()
	quarantineDir := filepath.Join(tmpDir, AppDirName, QuarantineDirName)
	filesDir := filepath.Join(quarantineDir, FilesSubDirName)
	manifestPath := filepath.Join(quarantineDir, ManifestFileName)
	_ = os.MkdirAll(filesDir, 0755)

	// 1. Create an untracked/orphan file in quarantine directory
	orphanDir := filepath.Join(filesDir, "unknown-op")
	_ = os.MkdirAll(orphanDir, 0755)
	orphanFile := filepath.Join(orphanDir, "mysterious.txt")
	_ = os.WriteFile(orphanFile, []byte("untracked file data"), 0644)

	// 2. Create an entry in manifest whose quarantine file has been altered
	alteredOpID := "op-altered"
	alteredDir := filepath.Join(filesDir, alteredOpID)
	_ = os.MkdirAll(alteredDir, 0755)
	alteredFile := filepath.Join(alteredDir, "altered.txt")
	_ = os.WriteFile(alteredFile, []byte("tampered content"), 0644)

	manifest := Manifest{
		Version: 1,
		Entries: map[string]Entry{
			alteredOpID: {
				ID:             alteredOpID,
				OriginalPath:   filepath.Join(tmpDir, "altered.txt"),
				QuarantinePath: alteredFile,
				Size:           100,
				SHA256:         "originalexpectedsha256hashwhichdoesnotmatch",
				Timestamp:      time.Now().UTC(),
				State:          StateQuarantined,
			},
			"op-missing": {
				ID:             "op-missing",
				OriginalPath:   filepath.Join(tmpDir, "missing.txt"),
				QuarantinePath: filepath.Join(filesDir, "op-missing", "missing.txt"),
				Size:           100,
				SHA256:         "somehash",
				Timestamp:      time.Now().UTC(),
				State:          StateQuarantined,
			},
		},
	}
	data, _ := json.MarshalIndent(manifest, "", "  ")
	_ = os.WriteFile(manifestPath, data, 0644)

	// Start manager
	_, report, err := NewManager(Options{BaseDir: tmpDir})
	if err != nil {
		t.Fatalf("startup failed: %v", err)
	}

	// Requirement 10: Never automatically purge quarantine contents
	if _, err := os.Stat(orphanFile); err != nil {
		t.Fatalf("untracked file was purged! It must be preserved: %v", err)
	}

	// Requirement 11: Preserve ambiguous files and report recovery warnings
	if len(report.UntrackedFiles) != 1 || report.UntrackedFiles[0] != orphanFile {
		t.Errorf("expected untracked file %s, got %v", orphanFile, report.UntrackedFiles)
	}

	// Check warnings reported for altered file, missing file, and untracked file
	var hasAlteredWarning, hasMissingWarning, hasUntrackedWarning bool
	for _, w := range report.Warnings {
		if strings.Contains(w, alteredOpID) {
			hasAlteredWarning = true
		}
		if strings.Contains(w, "op-missing") {
			hasMissingWarning = true
		}
		if strings.Contains(w, "untracked") {
			hasUntrackedWarning = true
		}
	}
	if !hasAlteredWarning {
		t.Errorf("missing warning for altered quarantined file")
	}
	if !hasMissingWarning {
		t.Errorf("missing warning for missing quarantined file")
	}
	if !hasUntrackedWarning {
		t.Errorf("missing warning for untracked file")
	}
}

func TestCorruptedManifest_SafeBackupAndPreservation(t *testing.T) {
	tmpDir := t.TempDir()
	quarantineDir := filepath.Join(tmpDir, AppDirName, QuarantineDirName)
	manifestPath := filepath.Join(quarantineDir, ManifestFileName)
	_ = os.MkdirAll(quarantineDir, 0755)

	// Write invalid JSON
	_ = os.WriteFile(manifestPath, []byte("{ incomplete json ..."), 0644)

	mgr, report, err := NewManager(Options{BaseDir: tmpDir})
	if err != nil {
		t.Fatalf("startup should succeed and handle corrupted manifest: %v", err)
	}

	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}

	var hasCorruptWarning bool
	for _, w := range report.Warnings {
		if strings.Contains(w, "corrupted manifest") {
			hasCorruptWarning = true
			break
		}
	}
	if !hasCorruptWarning {
		t.Errorf("expected warning for corrupted manifest, got %v", report.Warnings)
	}
}
