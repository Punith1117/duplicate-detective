package tui

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"duplicate-detective/internal/planner"
	"duplicate-detective/internal/quarantine"
	"duplicate-detective/internal/report"
	"duplicate-detective/internal/scanner"
)

// KeeperPlanner defines an interface for keeper selection and duplicate eligibility.
type KeeperPlanner interface {
	RecommendKeeper(group scanner.DuplicateGroup) string
	IsEligibleDuplicate(group scanner.DuplicateGroup, filePath string) bool
	ExplainRecommendation(group scanner.DuplicateGroup) string
}

// QuarantineManager defines an interface for managing quarantine actions and history.
type QuarantineManager interface {
	QuarantineFiles(root string, files []scanner.FileInfo) ([]QuarantineRecord, error)
	ListRecords(root string) ([]QuarantineRecord, error)
	RestoreRecord(root string, recordID string) error
	GetReconciliationWarnings(root string) ([]string, error)
}

// CSVExporter defines an interface for exporting duplicate reports.
type CSVExporter interface {
	Export(outPath string, result *scanner.ScanResult, keeperMap map[string]string, selectedMap map[string]bool) error
	ExportPlan(outPath string, plan *planner.Plan) error
}

// QuarantineRecord represents metadata for a quarantined file in the TUI.
type QuarantineRecord struct {
	ID             string     `json:"id"`
	OriginalPath   string     `json:"original_path"`
	QuarantinePath string     `json:"quarantine_path"`
	Size           int64      `json:"size"`
	Hash           string     `json:"hash"`
	QuarantinedAt  time.Time  `json:"quarantined_at"`
	RestoredAt     *time.Time `json:"restored_at,omitempty"`
	Restored       bool       `json:"restored"`
	State          string     `json:"state"`
	ErrorMessage   string     `json:"error_message,omitempty"`
}

// PlannerAdapter integrates internal/planner with the TUI.
type PlannerAdapter struct {
	BaseDir  string
	Strategy planner.Strategy
}

// NewPlannerAdapter creates a new PlannerAdapter backed by internal/planner.
func NewPlannerAdapter(baseDir string) *PlannerAdapter {
	return &PlannerAdapter{
		BaseDir:  baseDir,
		Strategy: planner.StrategyOldest,
	}
}

// RecommendKeeper returns the recommended keeper path using internal/planner.
func (p *PlannerAdapter) RecommendKeeper(group scanner.DuplicateGroup) string {
	if len(group.Files) == 0 {
		return ""
	}
	plan, err := planner.PlanDuplicates([]scanner.DuplicateGroup{group}, planner.Options{
		BaseDir:  p.BaseDir,
		Strategy: p.Strategy,
	})
	if err != nil || len(plan.Groups) == 0 {
		return group.Files[0].Path
	}
	return plan.Groups[0].RecommendedKeeper
}

// IsEligibleDuplicate returns whether a file is an eligible duplicate (not the keeper).
func (p *PlannerAdapter) IsEligibleDuplicate(group scanner.DuplicateGroup, filePath string) bool {
	keeper := p.RecommendKeeper(group)
	return filePath != "" && filePath != keeper
}

// ExplainRecommendation returns the deterministic rationale for the keeper choice.
func (p *PlannerAdapter) ExplainRecommendation(group scanner.DuplicateGroup) string {
	if len(group.Files) == 0 {
		return ""
	}
	plan, err := planner.PlanDuplicates([]scanner.DuplicateGroup{group}, planner.Options{
		BaseDir:  p.BaseDir,
		Strategy: p.Strategy,
	})
	if err != nil || len(plan.Groups) == 0 {
		return ""
	}
	return plan.Groups[0].RecommendationReason
}

// QuarantineAdapter integrates internal/quarantine with the TUI.
type QuarantineAdapter struct {
	mu sync.Mutex
}

// NewQuarantineAdapter creates a new QuarantineAdapter backed by internal/quarantine.
func NewQuarantineAdapter() *QuarantineAdapter {
	return &QuarantineAdapter{}
}

func (q *QuarantineAdapter) getManager(root string) (*quarantine.Manager, *quarantine.ReconciliationReport, error) {
	if root == "" {
		root = "."
	}
	return quarantine.NewManager(quarantine.Options{
		BaseDir: root,
	})
}

// GetReconciliationWarnings returns warnings from startup reconciliation.
func (q *QuarantineAdapter) GetReconciliationWarnings(root string) ([]string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	_, report, err := q.getManager(root)
	if err != nil {
		return nil, err
	}
	if report == nil {
		return []string{}, nil
	}
	return report.Warnings, nil
}

// QuarantineFiles validates and relocates files to the quarantine vault.
func (q *QuarantineAdapter) QuarantineFiles(root string, files []scanner.FileInfo) ([]QuarantineRecord, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	mgr, _, err := q.getManager(root)
	if err != nil {
		return nil, fmt.Errorf("quarantine manager init failed: %w", err)
	}

	var records []QuarantineRecord
	var errs []error

	for _, fi := range files {
		if fi.Path == "" {
			continue
		}
		entry, qErr := mgr.Quarantine(fi.Path, fi.Size, fi.Hash)
		if qErr != nil {
			errs = append(errs, qErr)
			continue
		}

		rec := QuarantineRecord{
			ID:             entry.ID,
			OriginalPath:   entry.OriginalPath,
			QuarantinePath: entry.QuarantinePath,
			Size:           entry.Size,
			Hash:           entry.SHA256,
			QuarantinedAt:  entry.Timestamp,
			Restored:       entry.State == quarantine.StateRestored,
			State:          string(entry.State),
			ErrorMessage:   entry.ErrorMessage,
		}
		records = append(records, rec)
	}

	if len(errs) > 0 && len(records) == 0 {
		return nil, errs[0]
	}

	return records, nil
}

// ListRecords returns all quarantine entries ordered newest first.
func (q *QuarantineAdapter) ListRecords(root string) ([]QuarantineRecord, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	mgr, _, err := q.getManager(root)
	if err != nil {
		return nil, fmt.Errorf("quarantine manager init failed: %w", err)
	}

	entries := mgr.ListEntries()
	records := make([]QuarantineRecord, 0, len(entries))

	for _, e := range entries {
		var restAt *time.Time
		if e.State == quarantine.StateRestored {
			t := e.Timestamp
			restAt = &t
		}

		records = append(records, QuarantineRecord{
			ID:             e.ID,
			OriginalPath:   e.OriginalPath,
			QuarantinePath: e.QuarantinePath,
			Size:           e.Size,
			Hash:           e.SHA256,
			QuarantinedAt:  e.Timestamp,
			RestoredAt:     restAt,
			Restored:       e.State == quarantine.StateRestored,
			State:          string(e.State),
			ErrorMessage:   e.ErrorMessage,
		})
	}

	// Sort newest first
	sort.Slice(records, func(i, j int) bool {
		return records[i].QuarantinedAt.After(records[j].QuarantinedAt)
	})

	return records, nil
}

// RestoreRecord restores a quarantined file using internal/quarantine.
// It strictly refuses to overwrite existing files at the destination.
func (q *QuarantineAdapter) RestoreRecord(root string, recordID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	mgr, _, err := q.getManager(root)
	if err != nil {
		return fmt.Errorf("quarantine manager init failed: %w", err)
	}

	entry, ok := mgr.GetEntry(recordID)
	if !ok {
		return errors.New("quarantine record not found")
	}

	if entry.State == quarantine.StateRestored {
		return errors.New("file is already restored")
	}

	_, err = mgr.Restore(recordID)
	return err
}

// ReportCSVExporter integrates internal/report with the TUI.
type ReportCSVExporter struct{}

// NewReportCSVExporter creates a new ReportCSVExporter backed by internal/report.
func NewReportCSVExporter() *ReportCSVExporter {
	return &ReportCSVExporter{}
}

// ExportPlan writes the CSV report directly from a planner.Plan.
func (e *ReportCSVExporter) ExportPlan(outPath string, plan *planner.Plan) error {
	if outPath == "" {
		outPath = fmt.Sprintf("duplicate-detective-report-%s.csv", time.Now().Format("20060102-150405"))
	}
	return report.ExportCSVFile(outPath, plan)
}

// Export writes the CSV report using internal/report.
func (e *ReportCSVExporter) Export(outPath string, result *scanner.ScanResult, keeperMap map[string]string, selectedMap map[string]bool) error {
	if outPath == "" {
		outPath = fmt.Sprintf("duplicate-detective-report-%s.csv", time.Now().Format("20060102-150405"))
	}

	if result == nil {
		return report.ExportCSVFile(outPath, &planner.Plan{})
	}

	plan, err := planner.PlanDuplicates(result.Duplicates, planner.Options{
		Strategy: planner.StrategyOldest,
	})
	if err != nil {
		return err
	}

	// Apply selection map to plan
	for gi := range plan.Groups {
		for fi := range plan.Groups[gi].Files {
			p := plan.Groups[gi].Files[fi].Path
			if selectedMap[p] {
				plan.Groups[gi].Files[fi].Selected = true
			}
		}
	}
	plan.RecalculateSavings()

	return report.ExportCSVFile(outPath, plan)
}
