package scanner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// helper to create a file with specific content in dir
func createFile(t *testing.T, dir, relPath string, content []byte) string {
	t.Helper()
	fullPath := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		t.Fatalf("failed to create directory for %s: %v", relPath, err)
	}
	if err := os.WriteFile(fullPath, content, 0644); err != nil {
		t.Fatalf("failed to write file %s: %v", relPath, err)
	}
	return fullPath
}

func computeExpectedHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func TestScan_BasicDuplicates(t *testing.T) {
	tempDir := t.TempDir()

	contentA := []byte("apple duplicate content 12345")
	contentB := []byte("banana unique content 67890")
	contentC := []byte("cherry duplicate content xyz")

	// Duplicates of contentA
	fileA1 := createFile(t, tempDir, "a/file1.txt", contentA)
	fileA2 := createFile(t, tempDir, "b/sub/file2.txt", contentA)
	fileA3 := createFile(t, tempDir, "c/file3.txt", contentA)

	// Unique file
	createFile(t, tempDir, "unique.txt", contentB)

	// Duplicates of contentC
	fileC1 := createFile(t, tempDir, "c1.bin", contentC)
	fileC2 := createFile(t, tempDir, "nested/c2.bin", contentC)

	res, err := Scan(tempDir)
	if err != nil {
		t.Fatalf("Scan failed unexpectedly: %v", err)
	}

	if res.TotalFiles != 6 {
		t.Errorf("expected 6 total files, got %d", res.TotalFiles)
	}

	if len(res.Duplicates) != 2 {
		t.Fatalf("expected 2 duplicate groups, got %d", len(res.Duplicates))
	}

	hashA := computeExpectedHash(contentA)
	hashC := computeExpectedHash(contentC)

	groupMap := make(map[string]DuplicateGroup)
	for _, g := range res.Duplicates {
		groupMap[g.Hash] = g
	}

	gA, ok := groupMap[hashA]
	if !ok {
		t.Fatalf("missing duplicate group for contentA (hash: %s)", hashA)
	}
	if len(gA.Files) != 3 {
		t.Errorf("expected 3 files in group A, got %d", len(gA.Files))
	}
	if gA.Size != int64(len(contentA)) {
		t.Errorf("expected group A size %d, got %d", len(contentA), gA.Size)
	}
	expectedPathsA := []string{fileA1, fileA2, fileA3}
	sort.Strings(expectedPathsA)
	for i, f := range gA.Files {
		if f.Path != expectedPathsA[i] {
			t.Errorf("group A file %d: expected %s, got %s", i, expectedPathsA[i], f.Path)
		}
		if f.Hash != hashA {
			t.Errorf("expected file hash %s, got %s", hashA, f.Hash)
		}
	}

	gC, ok := groupMap[hashC]
	if !ok {
		t.Fatalf("missing duplicate group for contentC (hash: %s)", hashC)
	}
	if len(gC.Files) != 2 {
		t.Errorf("expected 2 files in group C, got %d", len(gC.Files))
	}
	expectedPathsC := []string{fileC1, fileC2}
	sort.Strings(expectedPathsC)
	for i, f := range gC.Files {
		if f.Path != expectedPathsC[i] {
			t.Errorf("group C file %d: expected %s, got %s", i, expectedPathsC[i], f.Path)
		}
	}

	// Verify ScanResult helper methods
	if res.TotalDuplicateFiles() != (2 + 1) { // 2 redundant from group A, 1 redundant from group C
		t.Errorf("expected 3 redundant files, got %d", res.TotalDuplicateFiles())
	}
	expectedWasted := int64(2)*int64(len(contentA)) + int64(1)*int64(len(contentC))
	if res.TotalWastedSpace() != expectedWasted {
		t.Errorf("expected wasted space %d, got %d", expectedWasted, res.TotalWastedSpace())
	}
}

func TestScan_EmptyFiles(t *testing.T) {
	tempDir := t.TempDir()

	// Create 3 empty files
	empty1 := createFile(t, tempDir, "empty1.txt", []byte{})
	empty2 := createFile(t, tempDir, "sub/empty2.txt", []byte{})
	empty3 := createFile(t, tempDir, "another/empty3.txt", []byte{})

	// Create a non-empty unique file
	createFile(t, tempDir, "non_empty.txt", []byte("hello"))

	res, err := Scan(tempDir)
	if err != nil {
		t.Fatalf("Scan failed unexpectedly: %v", err)
	}

	if len(res.Duplicates) != 1 {
		t.Fatalf("expected 1 duplicate group for empty files, got %d", len(res.Duplicates))
	}

	g := res.Duplicates[0]
	if g.Size != 0 {
		t.Errorf("expected group size 0, got %d", g.Size)
	}

	emptyHash := computeExpectedHash([]byte{})
	if g.Hash != emptyHash {
		t.Errorf("expected empty file SHA-256 %s, got %s", emptyHash, g.Hash)
	}

	if len(g.Files) != 3 {
		t.Fatalf("expected 3 files in empty duplicate group, got %d", len(g.Files))
	}

	expectedPaths := []string{empty1, empty2, empty3}
	sort.Strings(expectedPaths)
	for i, f := range g.Files {
		if f.Path != expectedPaths[i] {
			t.Errorf("empty file %d: expected %s, got %s", i, expectedPaths[i], f.Path)
		}
	}
}

func TestScan_SingleEmptyFileNotReported(t *testing.T) {
	tempDir := t.TempDir()

	createFile(t, tempDir, "only_empty.txt", []byte{})
	createFile(t, tempDir, "file1.txt", []byte("file1"))

	res, err := Scan(tempDir)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	if len(res.Duplicates) != 0 {
		t.Errorf("expected 0 duplicate groups, got %d", len(res.Duplicates))
	}
}

func TestScan_CandidateGroupingBySizeBeforeHash(t *testing.T) {
	tempDir := t.TempDir()

	// Same size (10 bytes), but completely different contents
	content1 := []byte("0123456789")
	content2 := []byte("abcdefghij")
	content3 := []byte("9876543210")

	createFile(t, tempDir, "file1.txt", content1)
	createFile(t, tempDir, "file2.txt", content2)
	createFile(t, tempDir, "file3.txt", content3)

	res, err := Scan(tempDir)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	// Because all hashes differ, no duplicate group should be returned
	if len(res.Duplicates) != 0 {
		t.Errorf("expected 0 duplicate groups, got %d", len(res.Duplicates))
	}

	// Files with unique sizes should not even collide in candidate size map
	createFile(t, tempDir, "unique1.txt", []byte("1"))
	createFile(t, tempDir, "unique2.txt", []byte("12"))
	createFile(t, tempDir, "unique3.txt", []byte("123"))

	res2, err := Scan(tempDir)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	if len(res2.Duplicates) != 0 {
		t.Errorf("expected 0 duplicate groups, got %d", len(res2.Duplicates))
	}
}

func TestScan_ExcludeDuplicateDetectiveDirectory(t *testing.T) {
	tempDir := t.TempDir()

	commonData := []byte("common duplicate content")

	// Regular files outside
	f1 := createFile(t, tempDir, "outside/file1.txt", commonData)
	f2 := createFile(t, tempDir, "outside/file2.txt", commonData)

	// File inside top-level .duplicate-detective
	createFile(t, tempDir, ".duplicate-detective/cached1.txt", commonData)
	createFile(t, tempDir, ".duplicate-detective/cached2.txt", commonData)

	// File inside nested .duplicate-detective
	createFile(t, tempDir, "nested/.duplicate-detective/deep_cache.txt", commonData)

	res, err := Scan(tempDir)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	if len(res.Duplicates) != 1 {
		t.Fatalf("expected 1 duplicate group, got %d", len(res.Duplicates))
	}

	group := res.Duplicates[0]
	if len(group.Files) != 2 {
		t.Fatalf("expected exactly 2 files (excluding .duplicate-detective), got %d", len(group.Files))
	}

	for _, f := range group.Files {
		if strings.Contains(f.Path, ".duplicate-detective") {
			t.Errorf("unexpected file from .duplicate-detective found: %s", f.Path)
		}
	}

	expectedPaths := []string{f1, f2}
	sort.Strings(expectedPaths)
	for i, f := range group.Files {
		if f.Path != expectedPaths[i] {
			t.Errorf("file %d: expected %s, got %s", i, expectedPaths[i], f.Path)
		}
	}
}

func TestScan_ScanRootIsDuplicateDetective(t *testing.T) {
	tempDir := t.TempDir()
	appDir := filepath.Join(tempDir, ".duplicate-detective")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}
	createFile(t, appDir, "file1.txt", []byte("same"))
	createFile(t, appDir, "file2.txt", []byte("same"))

	res, err := Scan(appDir)
	if err != nil {
		t.Fatalf("Scan failed on app directory: %v", err)
	}
	if len(res.Duplicates) != 0 {
		t.Errorf("expected 0 duplicates when scanning app directory root, got %d", len(res.Duplicates))
	}
}

func TestScan_SymlinksNotFollowed(t *testing.T) {
	tempDir := t.TempDir()

	content := []byte("symlink test content")
	realFile := createFile(t, tempDir, "real/original.txt", content)

	// Create symlink to regular file
	symlinkToFile := filepath.Join(tempDir, "link_to_file.txt")
	if err := os.Symlink(realFile, symlinkToFile); err != nil {
		t.Skipf("skipping symlink test (symlink creation not supported): %v", err)
	}

	// Create symlink to directory containing a duplicate
	dupDir := filepath.Join(tempDir, "target_dir")
	createFile(t, dupDir, "in_target.txt", content)
	symlinkToDir := filepath.Join(tempDir, "link_to_dir")
	if err := os.Symlink(dupDir, symlinkToDir); err != nil {
		t.Skipf("skipping symlink directory test: %v", err)
	}

	res, err := Scan(tempDir)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	// Only realFile and target_dir/in_target.txt should be recognized as regular files
	if len(res.Duplicates) != 1 {
		t.Fatalf("expected 1 duplicate group, got %d", len(res.Duplicates))
	}

	group := res.Duplicates[0]
	if len(group.Files) != 2 {
		t.Fatalf("expected 2 files in group (ignoring symlinks), got %d", len(group.Files))
	}

	for _, f := range group.Files {
		if f.Path == symlinkToFile || strings.HasPrefix(f.Path, symlinkToDir) {
			t.Errorf("symlink was traversed: %s", f.Path)
		}
	}
}

func TestScan_RootIsSymlink(t *testing.T) {
	tempDir := t.TempDir()
	realDir := filepath.Join(tempDir, "real_dir")
	createFile(t, realDir, "f.txt", []byte("test"))

	linkDir := filepath.Join(tempDir, "link_dir")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("skipping symlink test: %v", err)
	}

	_, err := Scan(linkDir)
	if err == nil {
		t.Fatal("expected error when root is a symlink, got nil")
	}
}

func TestScan_UnreadableFilesAsWarnings(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("skipping unreadable file test as root user ignores 0000 permissions")
	}

	tempDir := t.TempDir()

	content := []byte("readable duplicate content")
	createFile(t, tempDir, "readable1.txt", content)
	createFile(t, tempDir, "readable2.txt", content)

	// Create an unreadable file with same size as readable content
	unreadablePath := filepath.Join(tempDir, "unreadable.txt")
	unreadableData := make([]byte, len(content))
	copy(unreadableData, "unreadable payload data!!!")
	if err := os.WriteFile(unreadablePath, unreadableData, 0000); err != nil {
		t.Fatalf("failed to create unreadable file: %v", err)
	}
	defer os.Chmod(unreadablePath, 0644) // cleanup permissions

	res, err := Scan(tempDir)
	if err != nil {
		t.Fatalf("scan should not crash on unreadable file, but got error: %v", err)
	}

	if len(res.Warnings) == 0 {
		t.Error("expected at least one warning for unreadable file, got 0")
	}

	var foundWarning bool
	for _, w := range res.Warnings {
		if strings.Contains(w, "unreadable.txt") {
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Errorf("warning for unreadable.txt not found in warnings: %v", res.Warnings)
	}

	// The two readable duplicates should still be identified
	if len(res.Duplicates) != 1 {
		t.Fatalf("expected 1 duplicate group for readable files, got %d", len(res.Duplicates))
	}
	if len(res.Duplicates[0].Files) != 2 {
		t.Errorf("expected 2 files in readable duplicate group, got %d", len(res.Duplicates[0].Files))
	}
}

func TestScan_InvalidRoot(t *testing.T) {
	tempDir := t.TempDir()

	// Empty path
	_, err := Scan("")
	if err == nil {
		t.Error("expected error for empty root path, got nil")
	}

	// Non-existent path
	_, err = Scan(filepath.Join(tempDir, "does-not-exist"))
	if err == nil {
		t.Error("expected error for non-existent root path, got nil")
	}

	// Root path is a regular file
	regularFilePath := createFile(t, tempDir, "not_a_dir.txt", []byte("hello"))
	_, err = Scan(regularFilePath)
	if err == nil {
		t.Error("expected error when root path is a file, got nil")
	}
}

func TestScan_LargeStreaming(t *testing.T) {
	tempDir := t.TempDir()

	// 128 KB of deterministic repeated data
	largeData := make([]byte, 128*1024)
	for i := range largeData {
		largeData[i] = byte(i % 251)
	}

	f1 := createFile(t, tempDir, "sub1/large1.bin", largeData)
	f2 := createFile(t, tempDir, "sub2/large2.bin", largeData)

	res, err := Scan(tempDir)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	if len(res.Duplicates) != 1 {
		t.Fatalf("expected 1 duplicate group for large files, got %d", len(res.Duplicates))
	}

	expectedHash := computeExpectedHash(largeData)
	if res.Duplicates[0].Hash != expectedHash {
		t.Errorf("expected hash %s, got %s", expectedHash, res.Duplicates[0].Hash)
	}
	if res.Duplicates[0].Size != int64(len(largeData)) {
		t.Errorf("expected size %d, got %d", len(largeData), res.Duplicates[0].Size)
	}
	if len(res.Duplicates[0].Files) != 2 {
		t.Errorf("expected 2 files, got %d", len(res.Duplicates[0].Files))
	}
	paths := res.Duplicates[0].FilePaths()
	if len(paths) != 2 || paths[0] != f1 || paths[1] != f2 {
		t.Errorf("unexpected file paths: %v", paths)
	}
}

func TestScan_ContextCancellation(t *testing.T) {
	tempDir := t.TempDir()

	data := []byte("cancellation test")
	createFile(t, tempDir, "f1.txt", data)
	createFile(t, tempDir, "f2.txt", data)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := ScanWithContext(ctx, tempDir)
	if err == nil {
		t.Fatal("expected error with canceled context, got nil")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("expected context canceled error, got %v", err)
	}
}

func TestScan_CustomExcludedDirs(t *testing.T) {
	tempDir := t.TempDir()

	data := []byte("custom exclude data")
	createFile(t, tempDir, "normal/f1.txt", data)
	createFile(t, tempDir, "normal/f2.txt", data)
	createFile(t, tempDir, "node_modules/pkg/f3.txt", data)

	scanner := New()
	scanner.ExcludedDirs = []string{"node_modules"}

	res, err := scanner.Scan(tempDir)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	if len(res.Duplicates) != 1 {
		t.Fatalf("expected 1 duplicate group, got %d", len(res.Duplicates))
	}

	for _, f := range res.Duplicates[0].Files {
		if strings.Contains(f.Path, "node_modules") {
			t.Errorf("unexpected node_modules file found: %s", f.Path)
		}
	}
}
