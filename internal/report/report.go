package report

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"duplicate-detective/internal/planner"
)

// CSVHeader defines the exact columns required for duplicate reporting.
var CSVHeader = []string{
	"group_id",
	"path",
	"size",
	"hash",
	"keeper",
	"selection",
	"savings",
}

// Row represents a single file entry in the CSV export report.
type Row struct {
	GroupID   string `json:"group_id"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Hash      string `json:"hash"`
	Keeper    bool   `json:"keeper"`
	Selection bool   `json:"selection"`
	Savings   int64  `json:"savings"`
}

// Summary summarizes the statistics of the report.
type Summary struct {
	TotalGroups      int   `json:"total_groups"`
	TotalFiles       int   `json:"total_files"`
	SelectedFiles    int   `json:"selected_files"`
	PotentialSavings int64 `json:"potential_savings"`
	ActualSavings    int64 `json:"actual_savings"`
}

// Report holds the structured data ready for export.
type Report struct {
	Rows    []Row   `json:"rows"`
	Summary Summary `json:"summary"`
}

// FromPlan converts a planner.Plan into a structured Report.
// For each file:
// - keeper is true if the file was recommended or designated as the keeper.
// - selection is true if the file is marked for cleanup.
// - savings represents the actual bytes saved by selecting that file (0 if kept).
func FromPlan(plan *planner.Plan) *Report {
	if plan == nil {
		return &Report{
			Rows: []Row{},
		}
	}

	rep := &Report{
		Rows: make([]Row, 0, plan.TotalFiles),
		Summary: Summary{
			TotalGroups:      len(plan.Groups),
			TotalFiles:       plan.TotalFiles,
			PotentialSavings: plan.PotentialSavings,
		},
	}

	for _, g := range plan.Groups {
		for _, f := range g.Files {
			var savings int64
			if f.Selected {
				savings = f.Size
				rep.Summary.SelectedFiles++
				rep.Summary.ActualSavings += f.Size
			}

			row := Row{
				GroupID:   g.GroupID,
				Path:      f.Path,
				Size:      f.Size,
				Hash:      f.Hash,
				Keeper:    f.IsKeeper,
				Selection: f.Selected,
				Savings:   savings,
			}
			rep.Rows = append(rep.Rows, row)
		}
	}

	return rep
}

// ExportCSV writes the CSV representation of the report to an io.Writer.
func (r *Report) ExportCSV(w io.Writer) error {
	writer := csv.NewWriter(w)
	defer writer.Flush()

	if err := writer.Write(CSVHeader); err != nil {
		return fmt.Errorf("report: failed to write CSV header: %w", err)
	}

	for _, row := range r.Rows {
		record := []string{
			row.GroupID,
			row.Path,
			strconv.FormatInt(row.Size, 10),
			row.Hash,
			strconv.FormatBool(row.Keeper),
			strconv.FormatBool(row.Selection),
			strconv.FormatInt(row.Savings, 10),
		}
		if err := writer.Write(record); err != nil {
			return fmt.Errorf("report: failed to write CSV record: %w", err)
		}
	}

	writer.Flush()
	return writer.Error()
}

// ExportCSVFile writes the CSV report directly to a destination file path.
func (r *Report) ExportCSVFile(filePath string) error {
	cleanPath := filepath.Clean(filePath)
	if err := os.MkdirAll(filepath.Dir(cleanPath), 0755); err != nil {
		return fmt.Errorf("report: failed to create directory for CSV file: %w", err)
	}

	f, err := os.Create(cleanPath)
	if err != nil {
		return fmt.Errorf("report: failed to create CSV file %q: %w", cleanPath, err)
	}
	defer f.Close()

	if err := r.ExportCSV(f); err != nil {
		return err
	}
	return f.Sync()
}

// ExportCSV is a convenience function that creates and writes a CSV report directly from a plan.
func ExportCSV(w io.Writer, plan *planner.Plan) error {
	return FromPlan(plan).ExportCSV(w)
}

// ExportCSVFile is a convenience function that writes a CSV report file directly from a plan.
func ExportCSVFile(filePath string, plan *planner.Plan) error {
	return FromPlan(plan).ExportCSVFile(filePath)
}

