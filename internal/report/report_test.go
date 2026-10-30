package report

import (
	"bytes"
	"encoding/csv"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"duplicate-detective/internal/planner"
	"duplicate-detective/internal/scanner"
)

func TestReportCSVExport(t *testing.T) {
	now := time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)
	group1 := scanner.DuplicateGroup{
		Hash: "hash-alpha",
		Size: 1024,
		Files: []scanner.FileInfo{
			{Path: "/data/short.txt", Size: 1024, ModTime: now},
			{Path: "/data/nested/duplicate.txt", Size: 1024, ModTime: now},
			{Path: "/data/other/copy.txt", Size: 1024, ModTime: now},
		},
	}

	plan, err := planner.PlanDuplicates([]scanner.DuplicateGroup{group1}, planner.Options{
		BaseDir: "/data",
	})
	if err != nil {
		t.Fatalf("failed to create plan: %v", err)
	}

	// Select one duplicate for cleanup (/data/nested/duplicate.txt)
	if err := plan.SetSelection("/data/nested/duplicate.txt", true); err != nil {
		t.Fatalf("failed to set selection: %v", err)
	}

	rep := FromPlan(plan)
	if rep.Summary.TotalFiles != 3 {
		t.Errorf("expected 3 total files, got %d", rep.Summary.TotalFiles)
	}
	if rep.Summary.SelectedFiles != 1 {
		t.Errorf("expected 1 selected file, got %d", rep.Summary.SelectedFiles)
	}
	if rep.Summary.ActualSavings != 1024 {
		t.Errorf("expected 1024 actual savings, got %d", rep.Summary.ActualSavings)
	}

	// Export to buffer
	var buf bytes.Buffer
	if err := rep.ExportCSV(&buf); err != nil {
		t.Fatalf("ExportCSV failed: %v", err)
	}

	reader := csv.NewReader(&buf)
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("failed to parse generated CSV: %v", err)
	}

	// Expected: 1 header row + 3 data rows
	if len(records) != 4 {
		t.Fatalf("expected 4 rows (1 header + 3 data), got %d", len(records))
	}

	header := records[0]
	expectedHeader := []string{"group_id", "path", "size", "hash", "keeper", "selection", "savings"}
	if len(header) != len(expectedHeader) {
		t.Fatalf("header length mismatch: expected %d, got %d", len(expectedHeader), len(header))
	}
	for i, h := range expectedHeader {
		if header[i] != h {
			t.Errorf("header column %d: expected %s, got %s", i, h, header[i])
		}
	}

	// Verify rows
	foundKeeper := false
	foundSelected := false
	for _, row := range records[1:] {
		groupID := row[0]
		path := row[1]
		size, _ := strconv.ParseInt(row[2], 10, 64)
		hash := row[3]
		keeper, _ := strconv.ParseBool(row[4])
		selection, _ := strconv.ParseBool(row[5])
		savings, _ := strconv.ParseInt(row[6], 10, 64)

		if groupID != "group-1" {
			t.Errorf("expected group-1, got %s", groupID)
		}
		if size != 1024 {
			t.Errorf("expected size 1024, got %d", size)
		}
		if hash != "hash-alpha" {
			t.Errorf("expected hash-alpha, got %s", hash)
		}

		if keeper {
			foundKeeper = true
			if path != "/data/short.txt" {
				t.Errorf("expected /data/short.txt as keeper, got %s", path)
			}
			if selection {
				t.Errorf("keeper should not be selected")
			}
			if savings != 0 {
				t.Errorf("keeper savings should be 0, got %d", savings)
			}
		}

		if selection {
			foundSelected = true
			if path != "/data/nested/duplicate.txt" {
				t.Errorf("expected /data/nested/duplicate.txt to be selected, got %s", path)
			}
			if savings != 1024 {
				t.Errorf("selected file savings should be 1024, got %d", savings)
			}
		}
	}

	if !foundKeeper {
		t.Errorf("no keeper row found in CSV")
	}
	if !foundSelected {
		t.Errorf("no selected row found in CSV")
	}
}

func TestReportExportCSVFile(t *testing.T) {
	tmpDir := t.TempDir()
	csvPath := filepath.Join(tmpDir, "reports", "duplicates.csv")

	group := scanner.DuplicateGroup{
		Hash: "hash-file-test",
		Size: 50,
		Files: []scanner.FileInfo{
			{Path: "/test/1.txt", Size: 50},
			{Path: "/test/2.txt", Size: 50},
		},
	}

	plan, err := planner.PlanDuplicates([]scanner.DuplicateGroup{group}, planner.Options{
		AutoSelectDuplicates: true,
	})
	if err != nil {
		t.Fatalf("failed to plan: %v", err)
	}

	if err := ExportCSVFile(csvPath, plan); err != nil {
		t.Fatalf("ExportCSVFile failed: %v", err)
	}

	content, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatalf("failed to read written CSV file: %v", err)
	}

	if len(content) == 0 {
		t.Fatal("written CSV file is empty")
	}

	f, err := os.Open(csvPath)
	if err != nil {
		t.Fatalf("open csv: %v", err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if len(rows) != 3 { // 1 header + 2 files
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
}

