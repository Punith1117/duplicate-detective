package scanner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// AppDirName is the reserved directory name for the duplicate detective application data.
const AppDirName = ".duplicate-detective"

// FileInfo represents metadata of a scanned file.
type FileInfo struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	Hash    string    `json:"hash,omitempty"`
}

// DuplicateGroup represents a set of files that share identical size and content hash.
type DuplicateGroup struct {
	Hash  string     `json:"hash"`
	Size  int64      `json:"size"`
	Files []FileInfo `json:"files"`
	Paths []string   `json:"paths,omitempty"`
}

// FilePaths returns slice of file paths contained in the duplicate group.
func (g DuplicateGroup) FilePaths() []string {
	paths := make([]string, len(g.Files))
	for i, f := range g.Files {
		paths[i] = f.Path
	}
	return paths
}

// DuplicateCount returns the number of redundant files (excluding the original).
func (g DuplicateGroup) DuplicateCount() int {
	if len(g.Files) <= 1 {
		return 0
	}
	return len(g.Files) - 1
}

// WastedSpace returns the bytes consumed by duplicate copies (excluding the original).
func (g DuplicateGroup) WastedSpace() int64 {
	return int64(g.DuplicateCount()) * g.Size
}

// ScanResult contains the aggregated results of a directory scan.
type ScanResult struct {
	Duplicates []DuplicateGroup `json:"duplicates"`
	Groups     []DuplicateGroup `json:"groups,omitempty"`
	TotalFiles int64            `json:"total_files"`
	TotalSize  int64            `json:"total_size"`
	Warnings   []string         `json:"warnings"`
}

// TotalWastedSpace returns the cumulative wasted space across all duplicate groups.
func (r *ScanResult) TotalWastedSpace() int64 {
	var total int64
	for _, g := range r.Duplicates {
		total += g.WastedSpace()
	}
	return total
}

// TotalDuplicateFiles returns the total count of redundant duplicate files.
func (r *ScanResult) TotalDuplicateFiles() int {
	var total int
	for _, g := range r.Duplicates {
		total += g.DuplicateCount()
	}
	return total
}

// Scanner scans directories for duplicate files.
type Scanner struct {
	// ExcludedDirs contains additional directory names to ignore during scanning.
	ExcludedDirs []string
}

// New creates a new Scanner instance.
func New() *Scanner {
	return &Scanner{}
}

// Scan recursively scans root for duplicate files using a default Scanner.
func Scan(root string) (*ScanResult, error) {
	return New().ScanWithContext(context.Background(), root)
}

// ScanWithContext recursively scans root for duplicate files with context support using a default Scanner.
func ScanWithContext(ctx context.Context, root string) (*ScanResult, error) {
	return New().ScanWithContext(ctx, root)
}

// Scan recursively scans root for duplicate files.
func (s *Scanner) Scan(root string) (*ScanResult, error) {
	return s.ScanWithContext(context.Background(), root)
}

// ScanWithContext recursively scans root for duplicate files with cancellation support.
func (s *Scanner) ScanWithContext(ctx context.Context, root string) (*ScanResult, error) {
	if root == "" {
		return nil, errors.New("scanner: root directory path cannot be empty")
	}

	cleanRoot := filepath.Clean(root)

	rootInfo, err := os.Lstat(cleanRoot)
	if err != nil {
		return nil, fmt.Errorf("scanner: failed to access root %q: %w", root, err)
	}

	if rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("scanner: root %q is a symlink, symlinks are not followed", root)
	}

	if !rootInfo.IsDir() {
		return nil, fmt.Errorf("scanner: root path %q is not a directory", root)
	}

	// Exclude if root directory itself is the application directory
	if rootInfo.Name() == AppDirName {
		return &ScanResult{
			Duplicates: []DuplicateGroup{},
			Groups:     []DuplicateGroup{},
			Warnings:   []string{},
		}, nil
	}

	excludedSet := make(map[string]bool)
	excludedSet[AppDirName] = true
	if s != nil {
		for _, name := range s.ExcludedDirs {
			if name != "" {
				excludedSet[name] = true
			}
		}
	}

	var warnings []string
	sizeMap := make(map[int64][]FileInfo)
	var totalFiles int64
	var totalSize int64

	// Step 1: Walk directory tree, filtering files and grouping by size
	walkErr := filepath.WalkDir(cleanRoot, func(path string, d fs.DirEntry, err error) error {
		if ctx != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		}

		if err != nil {
			warnings = append(warnings, fmt.Sprintf("scanner: error accessing path %q: %v", path, err))
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		name := d.Name()
		if d.IsDir() {
			if path != cleanRoot && excludedSet[name] {
				return filepath.SkipDir
			}
			return nil
		}

		// Do not process symlinks or non-regular files
		if d.Type()&os.ModeSymlink != 0 || !d.Type().IsRegular() {
			return nil
		}

		// Skip if file itself is named as excluded app directory
		if excludedSet[name] {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("scanner: failed to read info for %q: %v", path, err))
			return nil
		}

		if !info.Mode().IsRegular() {
			return nil
		}

		fileSize := info.Size()
		totalFiles++
		totalSize += fileSize

		fi := FileInfo{
			Path:    path,
			Size:    fileSize,
			ModTime: info.ModTime(),
		}
		sizeMap[fileSize] = append(sizeMap[fileSize], fi)

		return nil
	})

	if walkErr != nil {
		return nil, walkErr
	}

	// Step 2: Group candidate files by size before hashing
	var candidateSizes []int64
	for size, files := range sizeMap {
		if len(files) >= 2 {
			candidateSizes = append(candidateSizes, size)
		}
	}

	// Sort candidate sizes descending for predictable ordering
	sort.Slice(candidateSizes, func(i, j int) bool {
		return candidateSizes[i] > candidateSizes[j]
	})

	var duplicateGroups []DuplicateGroup

	// Step 3 & 4: Stream SHA-256 hashing for candidates with identical sizes
	for _, size := range candidateSizes {
		candidates := sizeMap[size]
		hashMap := make(map[string][]FileInfo)

		for _, file := range candidates {
			if ctx != nil {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				default:
				}
			}

			hash, err := hashFileStreaming(file.Path)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("scanner: failed to hash file %q: %v", file.Path, err))
				continue
			}

			file.Hash = hash
			hashMap[hash] = append(hashMap[hash], file)
		}

		// Step 5: Collect groups with at least 2 files sharing identical hash
		var sortedHashes []string
		for hash, files := range hashMap {
			if len(files) >= 2 {
				sortedHashes = append(sortedHashes, hash)
			}
		}
		sort.Strings(sortedHashes)

		for _, hash := range sortedHashes {
			files := hashMap[hash]
			sort.Slice(files, func(i, j int) bool {
				return files[i].Path < files[j].Path
			})

			paths := make([]string, len(files))
			for i, f := range files {
				paths[i] = f.Path
			}

			group := DuplicateGroup{
				Hash:  hash,
				Size:  size,
				Files: files,
				Paths: paths,
			}
			duplicateGroups = append(duplicateGroups, group)
		}
	}

	if warnings == nil {
		warnings = []string{}
	}
	if duplicateGroups == nil {
		duplicateGroups = []DuplicateGroup{}
	}

	return &ScanResult{
		Duplicates: duplicateGroups,
		Groups:     duplicateGroups,
		TotalFiles: totalFiles,
		TotalSize:  totalSize,
		Warnings:   warnings,
	}, nil
}

// hashFileStreaming computes SHA-256 hash by streaming the file contents.
func hashFileStreaming(path string) (string, error) {
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
