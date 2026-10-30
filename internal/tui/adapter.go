package tui

import (
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"duplicate-detective/internal/scanner"
)

// KeeperPlanner defines an interface for keeper selection and duplicate eligibility.
// If an external planner package is introduced, it can implement this interface.
type KeeperPlanner interface {
	RecommendKeeper(group scanner.DuplicateGroup) string
	IsEligibleDuplicate(group scanner.DuplicateGroup, filePath string) bool
}

// QuarantineManager defines an interface for managing quarantine actions and history.
// If an external quarantine package is introduced, it can implement this interface.
type QuarantineManager interface {
	QuarantineFiles(root string, files []scanner.FileInfo) ([]QuarantineRecord, error)
	ListRecords(root string) ([]QuarantineRecord, error)
	RestoreRecord(root string, recordID string) error
}

// CSVExporter defines an interface for exporting duplicate reports.
type CSVExporter interface {
	Export(outPath string, result *scanner.ScanResult, keeperMap map[string]string, selectedMap map[string]bool) error
}

// DefaultKeeperPlanner implements KeeperPlanner using deterministic heuristics:
// The oldest file (by modification time) is chosen as keeper. If tied, the shortest path is chosen.
type DefaultKeeperPlanner struct{}

// NewDefaultKeeperPlanner creates a new DefaultKeeperPlanner.
func NewDefaultKeeperPlanner() *DefaultKeeperPlanner {
	return &DefaultKeeperPlanner{}
}

// RecommendKeeper returns the path of the keeper file for a duplicate group.
func (p *DefaultKeeperPlanner) RecommendKeeper(group scanner.DuplicateGroup) string {
	if len(group.Files) == 0 {
		return ""
	}

	best := group.Files[0]
	for i := 1; i < len(group.Files); i++ {
		candidate := group.Files[i]
		if candidate.ModTime.Before(best.ModTime) {
			best = candidate
		} else if candidate.ModTime.Equal(best.ModTime) {
			// Tie breaker: prefer shorter path length, then alphabetical
			if len(candidate.Path) < len(best.Path) || (len(candidate.Path) == len(best.Path) && candidate.Path < best.Path) {
				best = candidate
			}
		}
	}
	return best.Path
}

// IsEligibleDuplicate returns whether a file is an eligible duplicate (i.e. not the keeper).
func (p *DefaultKeeperPlanner) IsEligibleDuplicate(group scanner.DuplicateGroup, filePath string) bool {
	keeper := p.RecommendKeeper(group)
	return filePath != "" && filePath != keeper
}

// QuarantineRecord represents metadata for a quarantined file.
type QuarantineRecord struct {
	ID             string     `json:"id"`
	OriginalPath   string     `json:"original_path"`
	QuarantinePath string     `json:"quarantine_path"`
	Size           int64      `json:"size"`
	Hash           string     `json:"hash"`
	QuarantinedAt  time.Time  `json:"quarantined_at"`
	RestoredAt     *time.Time `json:"restored_at,omitempty"`
	Restored       bool       `json:"restored"`
}

// DefaultQuarantineManager stores quarantined files under <root>/.duplicate-detective/quarantine/
// and maintains a manifest.json tracking operations.
type DefaultQuarantineManager struct {
	mu sync.Mutex
}

// NewDefaultQuarantineManager creates a new DefaultQuarantineManager.
func NewDefaultQuarantineManager() *DefaultQuarantineManager {
	return &DefaultQuarantineManager{}
}

func getQuarantineDir(root string) string {
	if root == "" {
		root = "."
	}
	return filepath.Join(root, scanner.AppDirName, "quarantine")
}

func getManifestPath(root string) string {
	return filepath.Join(getQuarantineDir(root), "manifest.json")
}

func (m *DefaultQuarantineManager) loadManifest(root string) ([]QuarantineRecord, error) {
	manifestPath := getManifestPath(root)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []QuarantineRecord{}, nil
		}
		return nil, err
	}

	var records []QuarantineRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, err
	}
	return records, nil
}

func (m *DefaultQuarantineManager) saveManifest(root string, records []QuarantineRecord) error {
	manifestPath := getManifestPath(root)
	dir := filepath.Dir(manifestPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(manifestPath, data, 0644)
}

func generateID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// QuarantineFiles moves the specified files to the quarantine directory and records them in manifest.
func (m *DefaultQuarantineManager) QuarantineFiles(root string, files []scanner.FileInfo) ([]QuarantineRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	records, err := m.loadManifest(root)
	if err != nil {
		return nil, fmt.Errorf("failed to load quarantine manifest: %w", err)
	}

	qDir := getQuarantineDir(root)
	dataDir := filepath.Join(qDir, "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create quarantine directory: %w", err)
	}

	var newRecords []QuarantineRecord
	var errs []error

	for _, fi := range files {
		if fi.Path == "" {
			continue
		}

		// Ensure file exists
		if _, err := os.Stat(fi.Path); err != nil {
			errs = append(errs, fmt.Errorf("file %s not found: %w", fi.Path, err))
			continue
		}

		id := generateID()
		ext := filepath.Ext(fi.Path)
		targetName := fmt.Sprintf("%s_%d%s", id, time.Now().UnixNano(), ext)
		targetPath := filepath.Join(dataDir, targetName)

		// Move file to quarantine
		if err := os.Rename(fi.Path, targetPath); err != nil {
			// Fallback to copy and remove if cross-device link
			if errCopy := copyAndRemove(fi.Path, targetPath); errCopy != nil {
				errs = append(errs, fmt.Errorf("failed to quarantine %s: %w", fi.Path, errCopy))
				continue
			}
		}

		rec := QuarantineRecord{
			ID:             id,
			OriginalPath:   fi.Path,
			QuarantinePath: targetPath,
			Size:           fi.Size,
			Hash:           fi.Hash,
			QuarantinedAt:  time.Now(),
			Restored:       false,
		}

		records = append(records, rec)
		newRecords = append(newRecords, rec)
	}

	if err := m.saveManifest(root, records); err != nil {
		return newRecords, fmt.Errorf("failed to save quarantine manifest: %w", err)
	}

	if len(errs) > 0 && len(newRecords) == 0 {
		return nil, errs[0]
	}

	return newRecords, nil
}

// ListRecords returns all quarantine records for root.
func (m *DefaultQuarantineManager) ListRecords(root string) ([]QuarantineRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	records, err := m.loadManifest(root)
	if err != nil {
		return nil, err
	}

	// Sort newest first
	sort.Slice(records, func(i, j int) bool {
		return records[i].QuarantinedAt.After(records[j].QuarantinedAt)
	})

	return records, nil
}

// RestoreRecord restores a single quarantined file to its original path.
func (m *DefaultQuarantineManager) RestoreRecord(root string, recordID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	records, err := m.loadManifest(root)
	if err != nil {
		return fmt.Errorf("failed to load quarantine manifest: %w", err)
	}

	var foundIdx = -1
	for i, r := range records {
		if r.ID == recordID {
			foundIdx = i
			break
		}
	}

	if foundIdx == -1 {
		return errors.New("quarantine record not found")
	}

	rec := records[foundIdx]
	if rec.Restored {
		return errors.New("file is already restored")
	}

	if _, err := os.Stat(rec.QuarantinePath); err != nil {
		return fmt.Errorf("quarantined file not found at %s: %w", rec.QuarantinePath, err)
	}

	// Check if original path already has an existing file
	if _, err := os.Stat(rec.OriginalPath); err == nil {
		return fmt.Errorf("destination %s already exists, cannot overwrite", rec.OriginalPath)
	}

	// Ensure destination directory exists
	if err := os.MkdirAll(filepath.Dir(rec.OriginalPath), 0755); err != nil {
		return fmt.Errorf("failed to recreate destination directory: %w", err)
	}

	// Move file back
	if err := os.Rename(rec.QuarantinePath, rec.OriginalPath); err != nil {
		if errCopy := copyAndRemove(rec.QuarantinePath, rec.OriginalPath); errCopy != nil {
			return fmt.Errorf("failed to restore file to %s: %w", rec.OriginalPath, errCopy)
		}
	}

	now := time.Now()
	records[foundIdx].Restored = true
	records[foundIdx].RestoredAt = &now

	return m.saveManifest(root, records)
}

func copyAndRemove(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := out.ReadFrom(in); err != nil {
		return err
	}

	_ = in.Close()
	return os.Remove(src)
}

// DefaultCSVExporter implements CSVExporter.
type DefaultCSVExporter struct{}

// NewDefaultCSVExporter creates a new DefaultCSVExporter.
func NewDefaultCSVExporter() *DefaultCSVExporter {
	return &DefaultCSVExporter{}
}

// Export writes duplicate groups to a CSV file.
func (e *DefaultCSVExporter) Export(outPath string, result *scanner.ScanResult, keeperMap map[string]string, selectedMap map[string]bool) error {
	if outPath == "" {
		outPath = fmt.Sprintf("duplicate-detective-report-%s.csv", time.Now().Format("20060102-150405"))
	}

	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("failed to create CSV file: %w", err)
	}
	defer f.Close()

	writer := csv.NewWriter(f)
	defer writer.Flush()

	header := []string{"GroupIndex", "Hash", "FileSizeBytes", "FilePath", "IsKeeper", "SelectedForCleanup", "ModTime"}
	if err := writer.Write(header); err != nil {
		return err
	}

	if result == nil {
		return nil
	}

	for gIdx, group := range result.Duplicates {
		keeperPath := keeperMap[group.Hash]
		for _, file := range group.Files {
			isKeeper := "false"
			if file.Path == keeperPath {
				isKeeper = "true"
			}

			isSelected := "false"
			if selectedMap[file.Path] {
				isSelected = "true"
			}

			row := []string{
				strconv.Itoa(gIdx + 1),
				group.Hash,
				strconv.FormatInt(file.Size, 10),
				file.Path,
				isKeeper,
				isSelected,
				file.ModTime.Format(time.RFC3339),
			}
			if err := writer.Write(row); err != nil {
				return err
			}
		}
	}

	return nil
}
