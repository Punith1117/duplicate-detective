package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func createTestFiles(t *testing.T) (string, []string) {
	t.Helper()
	dir := t.TempDir()

	file1 := filepath.Join(dir, "original.txt")
	file2 := filepath.Join(dir, "copy1.txt")
	file3 := filepath.Join(dir, "copy2.txt")
	unique := filepath.Join(dir, "unique.txt")

	content := []byte("identical duplicate content for testing")
	uniqueContent := []byte("unique content")

	if err := os.WriteFile(file1, content, 0644); err != nil {
		t.Fatalf("failed to write file1: %v", err)
	}
	// Give file1 earlier mod time
	earlier := time.Now().Add(-1 * time.Hour)
	_ = os.Chtimes(file1, earlier, earlier)

	if err := os.WriteFile(file2, content, 0644); err != nil {
		t.Fatalf("failed to write file2: %v", err)
	}
	if err := os.WriteFile(file3, content, 0644); err != nil {
		t.Fatalf("failed to write file3: %v", err)
	}
	if err := os.WriteFile(unique, uniqueContent, 0644); err != nil {
		t.Fatalf("failed to write unique: %v", err)
	}

	return dir, []string{file1, file2, file3, unique}
}

func setupTestModel(t *testing.T, dir string) *Model {
	m := NewModel(dir)
	m.Width = 120
	m.Height = 35

	// Run scanner directly
	res, err := m.Scanner.Scan(dir)
	if err != nil {
		t.Fatalf("scanner error: %v", err)
	}

	// Deliver scanCompletedMsg
	m.Update(scanCompletedMsg{Result: res, Err: nil})
	return &m
}

func TestModelInitialization(t *testing.T) {
	m := NewModel("")
	if m.State != ViewStateInput {
		t.Fatalf("expected state ViewStateInput, got %v", m.State)
	}
	if m.ActivePane != PaneGroups {
		t.Fatalf("expected active pane PaneGroups, got %v", m.ActivePane)
	}
	if m.Width <= 0 || m.Height <= 0 {
		t.Fatalf("expected positive width and height, got w=%d h=%d", m.Width, m.Height)
	}
}

func TestKeeperProtectionAndSelection(t *testing.T) {
	dir, files := createTestFiles(t)
	m := setupTestModel(t, dir)

	if len(m.ScanResult.Duplicates) != 1 {
		t.Fatalf("expected 1 duplicate group, got %d", len(m.ScanResult.Duplicates))
	}

	group := m.ScanResult.Duplicates[0]
	keeperPath := m.Keepers[group.Hash]

	// Keeper should be file1 (oldest mod time)
	if keeperPath != files[0] {
		t.Errorf("expected keeper %s, got %s", files[0], keeperPath)
	}

	// Switch to files pane
	m.ActivePane = PaneFiles
	m.SelectedFileIdx = 0

	// Find index of keeper
	var keeperIdx int
	for i, f := range group.Files {
		if f.Path == keeperPath {
			keeperIdx = i
			break
		}
	}

	// Test 1: Trying to toggle selection on keeper should be PROTECTED and rejected
	m.SelectedFileIdx = keeperIdx
	m.Update(tea.KeyMsg{Type: tea.KeySpace})

	if m.SelectedFiles[keeperPath] {
		t.Fatalf("keeper file %s was selected, but it MUST be protected!", keeperPath)
	}
	if !m.StatusIsErr || !strings.Contains(m.StatusMsg, "Protected") {
		t.Errorf("expected protection warning status, got msg=%q isErr=%v", m.StatusMsg, m.StatusIsErr)
	}

	// Test 2: Select eligible duplicates with 'A'
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})

	// Keeper must still NOT be selected
	if m.SelectedFiles[keeperPath] {
		t.Fatalf("keeper file %s was selected after 'A', but must remain protected!", keeperPath)
	}

	// Other duplicate copies MUST be selected
	eligibleCount := 0
	for _, f := range group.Files {
		if f.Path != keeperPath {
			eligibleCount++
			if !m.SelectedFiles[f.Path] {
				t.Errorf("expected duplicate %s to be selected", f.Path)
			}
		}
	}

	if eligibleCount != 2 {
		t.Errorf("expected 2 eligible duplicates, found %d", eligibleCount)
	}

	count, bytes := m.CalculateSelectedSavings()
	if count != 2 {
		t.Errorf("expected 2 files selected, got %d", count)
	}
	if bytes != group.Size*2 {
		t.Errorf("expected %d bytes selected, got %d", group.Size*2, bytes)
	}
}

func TestArrowNavigation(t *testing.T) {
	dir, _ := createTestFiles(t)
	m := setupTestModel(t, dir)

	// In groups pane, press Down
	m.ActivePane = PaneGroups
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	// Only 1 group, so should stay 0
	if m.SelectedGroupIdx != 0 {
		t.Errorf("expected SelectedGroupIdx 0, got %d", m.SelectedGroupIdx)
	}

	// Press Right arrow to switch to files pane
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.ActivePane != PaneFiles {
		t.Fatalf("expected ActivePane PaneFiles after right arrow, got %v", m.ActivePane)
	}

	// In files pane, navigate down and up
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.SelectedFileIdx != 1 {
		t.Errorf("expected SelectedFileIdx 1 after KeyDown, got %d", m.SelectedFileIdx)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.SelectedFileIdx != 0 {
		t.Errorf("expected SelectedFileIdx 0 after KeyUp, got %d", m.SelectedFileIdx)
	}

	// Press Left arrow to switch back to groups pane
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.ActivePane != PaneGroups {
		t.Fatalf("expected ActivePane PaneGroups after left arrow, got %v", m.ActivePane)
	}
}

func TestEnterKeyBehavior(t *testing.T) {
	dir, _ := createTestFiles(t)
	m := setupTestModel(t, dir)

	// Press Enter in Groups pane -> should open files pane
	m.ActivePane = PaneGroups
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.ActivePane != PaneFiles {
		t.Fatalf("expected Enter in Groups pane to switch to PaneFiles, got %v", m.ActivePane)
	}

	// Esc in Files pane -> should return to Groups pane
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.ActivePane != PaneGroups {
		t.Fatalf("expected Esc in Files pane to switch back to PaneGroups, got %v", m.ActivePane)
	}
}

func TestCSVExport(t *testing.T) {
	dir, _ := createTestFiles(t)
	m := setupTestModel(t, dir)

	// Press 'E' to trigger export
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	if cmd == nil {
		t.Fatal("expected export command to be returned")
	}

	msg := cmd()
	exportMsg, ok := msg.(csvExportedMsg)
	if !ok {
		t.Fatalf("expected csvExportedMsg, got %T", msg)
	}

	if exportMsg.Err != nil {
		t.Fatalf("csv export failed: %v", exportMsg.Err)
	}

	defer os.Remove(exportMsg.Path)

	data, err := os.ReadFile(exportMsg.Path)
	if err != nil {
		t.Fatalf("failed to read exported CSV: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "group_id,path,size,hash,keeper,selection,savings") {
		t.Errorf("missing CSV header in: %s", content)
	}
	if !strings.Contains(content, "true") { // At least one keeper is marked true
		t.Errorf("expected keeper marked true in CSV")
	}
}

func TestQuarantineAndRestoreLifecycle(t *testing.T) {
	dir, files := createTestFiles(t)
	m := setupTestModel(t, dir)

	// Select eligible duplicates
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})

	// Trigger clean confirmation with 'C'
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if m.State != ViewStateConfirmClean {
		t.Fatalf("expected state ViewStateConfirmClean, got %v", m.State)
	}

	// Confirm quarantine with 'Y'
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("expected quarantine command")
	}

	// Execute quarantine command
	msg := cmd()
	qMsg, ok := msg.(quarantineCompletedMsg)
	if !ok {
		t.Fatalf("expected quarantineCompletedMsg, got %T", msg)
	}

	if qMsg.Err != nil {
		t.Fatalf("quarantine failed: %v", qMsg.Err)
	}
	if qMsg.Count != 2 {
		t.Fatalf("expected 2 files quarantined, got %d", qMsg.Count)
	}

	// Verify keeper still exists on disk!
	if _, err := os.Stat(files[0]); err != nil {
		t.Fatalf("keeper file %s was deleted or moved, MUST BE PRESERVED: %v", files[0], err)
	}

	// Verify duplicate copy1 was removed from original location
	if _, err := os.Stat(files[1]); !os.IsNotExist(err) {
		t.Fatalf("expected file %s to be quarantined, but still exists", files[1])
	}

	// Process completion message
	m.Update(qMsg)
	if m.State != ViewStateDashboard {
		t.Fatalf("expected state ViewStateDashboard after quarantine, got %v", m.State)
	}

	// Open Recovery history with 'R'
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if m.State != ViewStateRecovery {
		t.Fatalf("expected state ViewStateRecovery, got %v", m.State)
	}
	if len(m.RecoveryRecords) != 2 {
		t.Fatalf("expected 2 recovery records, got %d", len(m.RecoveryRecords))
	}

	// Restore selected record with 'U'
	m.RecoveryIdx = 0
	_, restoreCmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	if restoreCmd == nil {
		t.Fatal("expected restore command")
	}

	restoreMsg := restoreCmd()
	resMsg, ok := restoreMsg.(restoreCompletedMsg)
	if !ok {
		t.Fatalf("expected restoreCompletedMsg, got %T", restoreMsg)
	}
	if resMsg.Err != nil {
		t.Fatalf("restore failed: %v", resMsg.Err)
	}

	// Verify file is back on disk!
	if _, err := os.Stat(resMsg.Path); err != nil {
		t.Fatalf("restored file does not exist at %s: %v", resMsg.Path, err)
	}

	// Press Esc to return to dashboard
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.State != ViewStateDashboard {
		t.Fatalf("expected return to ViewStateDashboard, got %v", m.State)
	}
}

func TestViewRenderingDoesNotPanic(t *testing.T) {
	dir, _ := createTestFiles(t)
	m := setupTestModel(t, dir)

	// 1. Dashboard view
	m.State = ViewStateDashboard
	dashView := m.View()
	if !strings.Contains(dashView, "DUPLICATE DETECTIVE") {
		t.Errorf("dashboard view missing title: %s", dashView)
	}

	// 2. Input view
	m.State = ViewStateInput
	inputView := m.View()
	if !strings.Contains(inputView, "Directory to scan:") {
		t.Errorf("input view missing prompt: %s", inputView)
	}

	// 3. Scanning view
	m.State = ViewStateScanning
	scanView := m.View()
	if !strings.Contains(scanView, "Scanning") {
		t.Errorf("scanning view missing scanning text: %s", scanView)
	}

	// 4. Confirm clean view
	m.State = ViewStateConfirmClean
	confirmView := m.View()
	if !strings.Contains(confirmView, "CONFIRM QUARANTINE") {
		t.Errorf("confirm view missing title: %s", confirmView)
	}

	// 5. Recovery view
	m.State = ViewStateRecovery
	recView := m.View()
	if !strings.Contains(recView, "RECOVERY") {
		t.Errorf("recovery view missing title: %s", recView)
	}
}

func TestTextInputHandling(t *testing.T) {
	ti := NewTextInput("/tmp/test", "placeholder")

	// Type characters
	ti.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if ti.Value != "/tmp/tests" {
		t.Errorf("expected /tmp/tests, got %s", ti.Value)
	}

	// Backspace
	ti.HandleKey(tea.KeyMsg{Type: tea.KeyBackspace})
	if ti.Value != "/tmp/test" {
		t.Errorf("expected /tmp/test, got %s", ti.Value)
	}

	// Move cursor left
	ti.HandleKey(tea.KeyMsg{Type: tea.KeyLeft})
	ti.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
	if ti.Value != "/tmp/tesXt" {
		t.Errorf("expected /tmp/tesXt, got %s", ti.Value)
	}
}

func TestRecoveryInconsistenciesProduceWarnings(t *testing.T) {
	dir, _ := createTestFiles(t)

	// Inject an untracked file inside quarantine files directory
	qFilesDir := filepath.Join(dir, ".duplicate-detective", "quarantine", "files", "untracked-op")
	if err := os.MkdirAll(qFilesDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(qFilesDir, "orphan.txt"), []byte("orphan"), 0644); err != nil {
		t.Fatal(err)
	}

	m := setupTestModel(t, dir)

	// Open recovery view
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if m.State != ViewStateRecovery {
		t.Fatalf("expected ViewStateRecovery, got %v", m.State)
	}

	// Warnings should be populated
	if len(m.ReconciliationWarnings) == 0 {
		t.Fatalf("expected reconciliation warnings for untracked file, got 0")
	}

	view := m.View()
	if !strings.Contains(view, "RECOVERY WARNINGS") {
		t.Errorf("expected recovery view to display warnings header, got:\n%s", view)
	}
}
