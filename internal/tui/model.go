package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"duplicate-detective/internal/planner"
	"duplicate-detective/internal/scanner"
)

// ViewState represents the active screen or overlay.
type ViewState int

const (
	ViewStateInput ViewState = iota
	ViewStateScanning
	ViewStateDashboard
	ViewStateConfirmClean
	ViewStateRecovery
)

// ActivePane represents the focused panel on the dashboard.
type ActivePane int

const (
	PaneGroups ActivePane = iota
	PaneFiles
)

// Messages for Bubble Tea event loop
type startScanMsg struct{}

type scanCompletedMsg struct {
	Result *scanner.ScanResult
	Err    error
}

type quarantineCompletedMsg struct {
	Count int
	Bytes int64
	Err   error
}

type restoreCompletedMsg struct {
	ID   string
	Path string
	Err  error
}

type csvExportedMsg struct {
	Path string
	Err  error
}

type spinnerTickMsg time.Time

type statusClearMsg struct {
	ID int
}

// TextInput handles single-line directory path input with cursor management.
type TextInput struct {
	Value       string
	Cursor      int
	Placeholder string
}

func NewTextInput(initial, placeholder string) TextInput {
	return TextInput{
		Value:       initial,
		Cursor:      len(initial),
		Placeholder: placeholder,
	}
}

func (ti *TextInput) HandleKey(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyBackspace:
		if ti.Cursor > 0 && len(ti.Value) > 0 {
			ti.Value = ti.Value[:ti.Cursor-1] + ti.Value[ti.Cursor:]
			ti.Cursor--
			return true
		}
	case tea.KeyDelete:
		if ti.Cursor < len(ti.Value) {
			ti.Value = ti.Value[:ti.Cursor] + ti.Value[ti.Cursor+1:]
			return true
		}
	case tea.KeyLeft:
		if ti.Cursor > 0 {
			ti.Cursor--
			return true
		}
	case tea.KeyRight:
		if ti.Cursor < len(ti.Value) {
			ti.Cursor++
			return true
		}
	case tea.KeyHome:
		ti.Cursor = 0
		return true
	case tea.KeyEnd:
		ti.Cursor = len(ti.Value)
		return true
	case tea.KeyRunes:
		chars := string(msg.Runes)
		ti.Value = ti.Value[:ti.Cursor] + chars + ti.Value[ti.Cursor:]
		ti.Cursor += len(chars)
		return true
	}
	return false
}

// Model represents the overall TUI application state.
type Model struct {
	// Configuration & Services
	Scanner                *scanner.Scanner
	Planner                KeeperPlanner
	QuarantineManager      QuarantineManager
	Exporter               CSVExporter
	Plan                   *planner.Plan
	ReconciliationWarnings []string

	// View & Window
	State      ViewState
	PrevState  ViewState
	ActivePane ActivePane
	Width      int
	Height     int

	// Path input
	PathInput TextInput
	ScanPath  string
	AutoScan  bool

	// Scan state & results
	ScanResult     *scanner.ScanResult
	ScanCancel     context.CancelFunc
	ScanningDots   int
	Keepers        map[string]string // hash -> keeper file path
	SelectedFiles  map[string]bool   // file path -> selected for cleanup

	// Navigation indices
	SelectedGroupIdx int
	SelectedFileIdx  int
	GroupScroll      int
	FileScroll       int

	// Recovery state
	RecoveryRecords []QuarantineRecord
	RecoveryIdx     int
	RecoveryScroll  int

	// Status & notifications
	StatusMsg   string
	StatusIsErr bool
	StatusID    int

	// Flags
	QuarantineInProgress bool
	LastExportedFile     string
}

// NewModel creates a new Model with default implementations.
func NewModel(initialPath string) Model {
	if initialPath == "" {
		cwd, err := os.Getwd()
		if err == nil {
			initialPath = cwd
		} else {
			initialPath = "."
		}
	}

	absPath, err := filepath.Abs(initialPath)
	if err == nil {
		initialPath = absPath
	}

	m := Model{
		Scanner:           scanner.New(),
		Planner:           NewPlannerAdapter(initialPath),
		QuarantineManager: NewQuarantineAdapter(),
		Exporter:          NewReportCSVExporter(),
		State:             ViewStateInput,
		ActivePane:        PaneGroups,
		PathInput:         NewTextInput(initialPath, "Enter directory path to scan..."),
		ScanPath:          initialPath,
		Keepers:           make(map[string]string),
		SelectedFiles:     make(map[string]bool),
		Width:             100,
		Height:            30,
	}

	return m
}

// Init initializes the Bubble Tea program.
func (m *Model) Init() tea.Cmd {
	if m.AutoScan {
		return func() tea.Msg {
			return startScanMsg{}
		}
	}
	return nil
}

func spinnerTickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg {
		return spinnerTickMsg(t)
	})
}

func (m *Model) triggerStatus(msg string, isErr bool) tea.Cmd {
	m.StatusMsg = msg
	m.StatusIsErr = isErr
	m.StatusID++
	id := m.StatusID
	return tea.Tick(4*time.Second, func(t time.Time) tea.Msg {
		return statusClearMsg{ID: id}
	})
}

// StartScan triggers a scan on the configured ScanPath.
func (m *Model) StartScan() tea.Cmd {
	m.State = ViewStateScanning
	m.ScanResult = nil
	m.Keepers = make(map[string]string)
	m.SelectedFiles = make(map[string]bool)
	m.SelectedGroupIdx = 0
	m.SelectedFileIdx = 0
	m.GroupScroll = 0
	m.FileScroll = 0

	ctx, cancel := context.WithCancel(context.Background())
	m.ScanCancel = cancel

	path := m.ScanPath

	return tea.Batch(
		spinnerTickCmd(),
		func() tea.Msg {
			res, err := m.Scanner.ScanWithContext(ctx, path)
			return scanCompletedMsg{Result: res, Err: err}
		},
	)
}

// Update processes Bubble Tea messages.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height

	case startScanMsg:
		return m, m.StartScan()

	case spinnerTickMsg:
		if m.State == ViewStateScanning {
			m.ScanningDots = (m.ScanningDots + 1) % 4
			cmds = append(cmds, spinnerTickCmd())
		}

	case statusClearMsg:
		if msg.ID == m.StatusID {
			m.StatusMsg = ""
			m.StatusIsErr = false
		}

	case scanCompletedMsg:
		if msg.Err != nil {
			if msg.Err == context.Canceled {
				m.State = ViewStateInput
				cmds = append(cmds, m.triggerStatus("Scan cancelled by user.", false))
			} else {
				m.State = ViewStateInput
				cmds = append(cmds, m.triggerStatus("Scan error: "+msg.Err.Error(), true))
			}
			return m, tea.Batch(cmds...)
		}

		m.ScanResult = msg.Result
		m.State = ViewStateDashboard
		m.ActivePane = PaneGroups
		m.SelectedGroupIdx = 0
		m.SelectedFileIdx = 0
		m.GroupScroll = 0
		m.FileScroll = 0

		// Identify keepers and generate plan using internal/planner
		plan, err := planner.PlanDuplicates(m.ScanResult.Duplicates, planner.Options{
			BaseDir:  m.ScanPath,
			Strategy: planner.StrategyOldest,
		})
		if err == nil {
			m.Plan = plan
			for _, group := range plan.Groups {
				m.Keepers[group.Hash] = group.RecommendedKeeper
			}
		} else {
			for _, group := range m.ScanResult.Duplicates {
				keeper := m.Planner.RecommendKeeper(group)
				m.Keepers[group.Hash] = keeper
			}
		}

		// Retrieve any quarantine startup reconciliation warnings
		if warnings, err := m.QuarantineManager.GetReconciliationWarnings(m.ScanPath); err == nil && len(warnings) > 0 {
			m.ReconciliationWarnings = warnings
		}

		status := fmt.Sprintf("Found %d duplicate groups (%s potential savings).",
			len(m.ScanResult.Duplicates),
			FormatBytes(m.ScanResult.TotalWastedSpace()))
		if len(m.ReconciliationWarnings) > 0 {
			status += fmt.Sprintf(" (Notice: %d quarantine recovery warnings)", len(m.ReconciliationWarnings))
		}
		cmds = append(cmds, m.triggerStatus(status, false))

	case quarantineCompletedMsg:
		m.QuarantineInProgress = false
		m.State = ViewStateDashboard
		if msg.Err != nil {
			cmds = append(cmds, m.triggerStatus("Quarantine failed: "+msg.Err.Error(), true))
			return m, tea.Batch(cmds...)
		}

		// Update duplicates in memory by filtering out quarantined files
		if m.ScanResult != nil {
			var remainingGroups []scanner.DuplicateGroup
			for _, group := range m.ScanResult.Duplicates {
				var remainingFiles []scanner.FileInfo
				for _, f := range group.Files {
					if !m.SelectedFiles[f.Path] {
						remainingFiles = append(remainingFiles, f)
					}
				}
				if len(remainingFiles) >= 2 {
					group.Files = remainingFiles
					group.Paths = group.FilePaths()
					remainingGroups = append(remainingGroups, group)
					// Refresh keeper if keeper was affected
					m.Keepers[group.Hash] = m.Planner.RecommendKeeper(group)
				}
			}
			m.ScanResult.Duplicates = remainingGroups
			m.ScanResult.Groups = remainingGroups
		}

		// Clear selected files map
		m.SelectedFiles = make(map[string]bool)
		m.SelectedFileIdx = 0
		if m.SelectedGroupIdx >= len(m.currentGroups()) {
			m.SelectedGroupIdx = len(m.currentGroups()) - 1
			if m.SelectedGroupIdx < 0 {
				m.SelectedGroupIdx = 0
			}
		}

		if m.ScanResult != nil {
			m.Plan, _ = planner.PlanDuplicates(m.ScanResult.Duplicates, planner.Options{
				BaseDir:  m.ScanPath,
				Strategy: planner.StrategyOldest,
			})
		}

		status := fmt.Sprintf("Quarantined %d files (%s reclaimed).", msg.Count, FormatBytes(msg.Bytes))
		cmds = append(cmds, m.triggerStatus(status, false))

	case restoreCompletedMsg:
		if msg.Err != nil {
			cmds = append(cmds, m.triggerStatus("Restore failed: "+msg.Err.Error(), true))
		} else {
			cmds = append(cmds, m.triggerStatus("Successfully restored: "+filepath.Base(msg.Path), false))
			// Refresh recovery records
			if recs, err := m.QuarantineManager.ListRecords(m.ScanPath); err == nil {
				m.RecoveryRecords = recs
			}
		}

	case csvExportedMsg:
		if msg.Err != nil {
			cmds = append(cmds, m.triggerStatus("Export error: "+msg.Err.Error(), true))
		} else {
			m.LastExportedFile = msg.Path
			cmds = append(cmds, m.triggerStatus("Report exported to: "+msg.Path, false))
		}

	case tea.KeyMsg:
		return m.handleKeyPress(msg)
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) currentGroups() []scanner.DuplicateGroup {
	if m.ScanResult == nil {
		return nil
	}
	return m.ScanResult.Duplicates
}

func (m *Model) currentGroup() *scanner.DuplicateGroup {
	groups := m.currentGroups()
	if len(groups) == 0 || m.SelectedGroupIdx < 0 || m.SelectedGroupIdx >= len(groups) {
		return nil
	}
	return &groups[m.SelectedGroupIdx]
}

func (m *Model) currentFiles() []scanner.FileInfo {
	group := m.currentGroup()
	if group == nil {
		return nil
	}
	return group.Files
}

func (m *Model) currentFile() *scanner.FileInfo {
	files := m.currentFiles()
	if len(files) == 0 || m.SelectedFileIdx < 0 || m.SelectedFileIdx >= len(files) {
		return nil
	}
	return &files[m.SelectedFileIdx]
}

// CalculateSelectedSavings returns total count and bytes of selected files.
func (m *Model) CalculateSelectedSavings() (int, int64) {
	if m.Plan != nil {
		count := 0
		for _, g := range m.Plan.Groups {
			for _, f := range g.Files {
				if f.Selected {
					count++
				}
			}
		}
		return count, m.Plan.SelectedSavings
	}

	count := 0
	var bytes int64
	if m.ScanResult == nil {
		return 0, 0
	}
	for _, group := range m.ScanResult.Duplicates {
		for _, f := range group.Files {
			if m.SelectedFiles[f.Path] {
				count++
				bytes += f.Size
			}
		}
	}
	return count, bytes
}

// handleKeyPress routes keyboard input based on current ViewState.
func (m *Model) handleKeyPress(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.State {
	case ViewStateInput:
		return m.handleInputKey(msg)
	case ViewStateScanning:
		return m.handleScanningKey(msg)
	case ViewStateDashboard:
		return m.handleDashboardKey(msg)
	case ViewStateConfirmClean:
		return m.handleConfirmKey(msg)
	case ViewStateRecovery:
		return m.handleRecoveryKey(msg)
	}
	return m, nil
}

func (m *Model) handleInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		if m.ScanResult != nil && len(m.ScanResult.Duplicates) > 0 {
			m.State = ViewStateDashboard
			return m, nil
		}
		return m, tea.Quit
	case tea.KeyEnter:
		path := strings.TrimSpace(m.PathInput.Value)
		if path == "" {
			return m, m.triggerStatus("Directory path cannot be empty.", true)
		}
		info, err := os.Stat(path)
		if err != nil {
			return m, m.triggerStatus("Directory does not exist: "+err.Error(), true)
		}
		if !info.IsDir() {
			return m, m.triggerStatus("Path is not a directory.", true)
		}
		m.ScanPath = path
		return m, m.StartScan()
	default:
		m.PathInput.HandleKey(msg)
	}
	return m, nil
}

func (m *Model) handleScanningKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		if m.ScanCancel != nil {
			m.ScanCancel()
		}
		return m, tea.Quit
	case tea.KeyEsc:
		if m.ScanCancel != nil {
			m.ScanCancel()
		}
		m.State = ViewStateInput
		return m, m.triggerStatus("Scan cancelled.", false)
	}
	return m, nil
}

func (m *Model) handleDashboardKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit

	case tea.KeyEsc:
		if m.ActivePane == PaneFiles {
			m.ActivePane = PaneGroups
			return m, nil
		}
		// If on groups pane, allow returning to input or prompt
		m.State = ViewStateInput
		return m, nil

	case tea.KeyUp:
		m.navigateUp()
		return m, nil

	case tea.KeyDown:
		m.navigateDown()
		return m, nil

	case tea.KeyLeft:
		if m.ActivePane == PaneFiles {
			m.ActivePane = PaneGroups
		}
		return m, nil

	case tea.KeyRight:
		if m.ActivePane == PaneGroups && len(m.currentFiles()) > 0 {
			m.ActivePane = PaneFiles
		}
		return m, nil

	case tea.KeyEnter:
		if m.ActivePane == PaneGroups {
			if len(m.currentFiles()) > 0 {
				m.ActivePane = PaneFiles
			}
		} else {
			// On files pane, Enter toggles selection
			return m.toggleCurrentFileSelection()
		}
		return m, nil

	case tea.KeySpace:
		if m.ActivePane == PaneFiles {
			return m.toggleCurrentFileSelection()
		}
		// In groups pane, Space toggles all eligible files in the current group
		return m.toggleCurrentGroupSelection()

	case tea.KeyRunes:
		k := strings.ToLower(string(msg.Runes))
		switch k {
		case "q":
			return m, tea.Quit
		case "a":
			// Select all eligible duplicates across all groups
			return m.selectAllEligible()
		case "e":
			// Export CSV
			return m.exportCSV()
		case "r":
			// Recovery history
			return m.openRecovery()
		case "u":
			// Restore shortcut
			return m.openRecovery()
		case "c":
			// Cleanup confirmation
			return m.openConfirmClean()
		case "/":
			// Change directory
			m.State = ViewStateInput
			return m, nil
		}
	}
	return m, nil
}

func (m *Model) navigateUp() {
	if m.ActivePane == PaneGroups {
		if m.SelectedGroupIdx > 0 {
			m.SelectedGroupIdx--
			m.SelectedFileIdx = 0
			m.FileScroll = 0
			if m.SelectedGroupIdx < m.GroupScroll {
				m.GroupScroll = m.SelectedGroupIdx
			}
		}
	} else {
		if m.SelectedFileIdx > 0 {
			m.SelectedFileIdx--
			if m.SelectedFileIdx < m.FileScroll {
				m.FileScroll = m.SelectedFileIdx
			}
		}
	}
}

func (m *Model) navigateDown() {
	if m.ActivePane == PaneGroups {
		groups := m.currentGroups()
		if m.SelectedGroupIdx < len(groups)-1 {
			m.SelectedGroupIdx++
			m.SelectedFileIdx = 0
			m.FileScroll = 0
			visibleRows := m.Height - 16
			if visibleRows < 5 {
				visibleRows = 5
			}
			if m.SelectedGroupIdx >= m.GroupScroll+visibleRows {
				m.GroupScroll = m.SelectedGroupIdx - visibleRows + 1
			}
		}
	} else {
		files := m.currentFiles()
		if m.SelectedFileIdx < len(files)-1 {
			m.SelectedFileIdx++
			visibleRows := m.Height - 16
			if visibleRows < 5 {
				visibleRows = 5
			}
			if m.SelectedFileIdx >= m.FileScroll+visibleRows {
				m.FileScroll = m.SelectedFileIdx - visibleRows + 1
			}
		}
	}
}

// toggleCurrentFileSelection toggles selection of the highlighted file, protecting keeper.
func (m *Model) toggleCurrentFileSelection() (tea.Model, tea.Cmd) {
	file := m.currentFile()
	group := m.currentGroup()
	if file == nil || group == nil {
		return m, nil
	}

	keeper := m.Keepers[group.Hash]
	if file.Path == keeper {
		return m, m.triggerStatus("Protected: Keeper file cannot be selected for deletion.", true)
	}

	target := !m.SelectedFiles[file.Path]
	if m.Plan != nil {
		if err := m.Plan.SetSelection(file.Path, target); err != nil {
			return m, m.triggerStatus("Safety protection: "+err.Error(), true)
		}
	}

	m.SelectedFiles[file.Path] = target
	if !m.SelectedFiles[file.Path] {
		delete(m.SelectedFiles, file.Path)
	}

	return m, nil
}

// toggleCurrentGroupSelection toggles selection of all eligible files in the group.
func (m *Model) toggleCurrentGroupSelection() (tea.Model, tea.Cmd) {
	group := m.currentGroup()
	if group == nil {
		return m, nil
	}

	keeper := m.Keepers[group.Hash]
	// Check if all eligible files are already selected
	allSelected := true
	for _, f := range group.Files {
		if f.Path == keeper {
			continue
		}
		if !m.SelectedFiles[f.Path] {
			allSelected = false
			break
		}
	}

	target := !allSelected
	for _, f := range group.Files {
		if f.Path == keeper {
			continue
		}
		if m.Plan != nil {
			_ = m.Plan.SetSelection(f.Path, target)
		}
		if target {
			m.SelectedFiles[f.Path] = true
		} else {
			delete(m.SelectedFiles, f.Path)
		}
	}

	return m, nil
}

// selectAllEligible selects all non-keeper files across all duplicate groups.
func (m *Model) selectAllEligible() (tea.Model, tea.Cmd) {
	if m.ScanResult == nil || len(m.ScanResult.Duplicates) == 0 {
		return m, nil
	}

	if m.Plan != nil {
		m.Plan.SelectNonKeepers()
		m.SelectedFiles = make(map[string]bool)
		for _, f := range m.Plan.SelectedFiles() {
			m.SelectedFiles[f.Path] = true
		}
		return m, m.triggerStatus(fmt.Sprintf("Selected %d eligible duplicates (keepers protected).", len(m.SelectedFiles)), false)
	}

	selectedCount := 0
	for _, group := range m.ScanResult.Duplicates {
		keeper := m.Keepers[group.Hash]
		for _, f := range group.Files {
			if f.Path == keeper {
				// Protect keeper!
				delete(m.SelectedFiles, f.Path)
				continue
			}
			m.SelectedFiles[f.Path] = true
			selectedCount++
		}
	}

	return m, m.triggerStatus(fmt.Sprintf("Selected %d eligible duplicates (keepers protected).", selectedCount), false)
}

func (m *Model) openConfirmClean() (tea.Model, tea.Cmd) {
	count, _ := m.CalculateSelectedSavings()
	if count == 0 {
		return m, m.triggerStatus("No files selected for cleanup. Press Space or 'A' to select.", true)
	}
	m.State = ViewStateConfirmClean
	return m, nil
}

func (m *Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC, tea.KeyEsc:
		m.State = ViewStateDashboard
		return m, nil
	case tea.KeyEnter:
		return m.executeQuarantine()
	case tea.KeyRunes:
		switch strings.ToLower(string(msg.Runes)) {
		case "y":
			return m.executeQuarantine()
		case "n", "q":
			m.State = ViewStateDashboard
			return m, nil
		}
	}
	return m, nil
}

func (m *Model) executeQuarantine() (tea.Model, tea.Cmd) {
	if m.QuarantineInProgress {
		return m, nil
	}

	var filesToClean []scanner.FileInfo
	for _, group := range m.ScanResult.Duplicates {
		for _, f := range group.Files {
			if m.SelectedFiles[f.Path] {
				filesToClean = append(filesToClean, f)
			}
		}
	}

	if len(filesToClean) == 0 {
		m.State = ViewStateDashboard
		return m, nil
	}

	m.QuarantineInProgress = true
	root := m.ScanPath

	return m, func() tea.Msg {
		records, err := m.QuarantineManager.QuarantineFiles(root, filesToClean)
		var totalBytes int64
		for _, r := range records {
			totalBytes += r.Size
		}
		return quarantineCompletedMsg{
			Count: len(records),
			Bytes: totalBytes,
			Err:   err,
		}
	}
}

func (m *Model) exportCSV() (tea.Model, tea.Cmd) {
	if m.ScanResult == nil || len(m.ScanResult.Duplicates) == 0 {
		return m, m.triggerStatus("No scan results to export.", true)
	}

	defaultName := fmt.Sprintf("duplicate-detective-report-%s.csv", time.Now().Format("20060102-150405"))
	outPath := filepath.Join(".", defaultName)

	if m.Plan != nil {
		plan := m.Plan
		return m, func() tea.Msg {
			err := m.Exporter.ExportPlan(outPath, plan)
			return csvExportedMsg{Path: outPath, Err: err}
		}
	}

	res := m.ScanResult
	keepers := m.Keepers
	selected := m.SelectedFiles

	return m, func() tea.Msg {
		err := m.Exporter.Export(outPath, res, keepers, selected)
		return csvExportedMsg{Path: outPath, Err: err}
	}
}

func (m *Model) openRecovery() (tea.Model, tea.Cmd) {
	if warnings, err := m.QuarantineManager.GetReconciliationWarnings(m.ScanPath); err == nil && len(warnings) > 0 {
		m.ReconciliationWarnings = warnings
	}

	records, err := m.QuarantineManager.ListRecords(m.ScanPath)
	if err != nil {
		return m, m.triggerStatus("Failed to load recovery history: "+err.Error(), true)
	}

	m.RecoveryRecords = records
	m.RecoveryIdx = 0
	m.RecoveryScroll = 0
	m.State = ViewStateRecovery
	return m, nil
}

func (m *Model) handleRecoveryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		m.State = ViewStateDashboard
		return m, nil
	case tea.KeyUp:
		if m.RecoveryIdx > 0 {
			m.RecoveryIdx--
			if m.RecoveryIdx < m.RecoveryScroll {
				m.RecoveryScroll = m.RecoveryIdx
			}
		}
		return m, nil
	case tea.KeyDown:
		if m.RecoveryIdx < len(m.RecoveryRecords)-1 {
			m.RecoveryIdx++
			visibleRows := m.Height - 12
			if visibleRows < 5 {
				visibleRows = 5
			}
			if m.RecoveryIdx >= m.RecoveryScroll+visibleRows {
				m.RecoveryScroll = m.RecoveryIdx - visibleRows + 1
			}
		}
		return m, nil
	case tea.KeyEnter:
		return m.restoreSelectedRecord()
	case tea.KeyRunes:
		k := strings.ToLower(string(msg.Runes))
		switch k {
		case "u":
			return m.restoreSelectedRecord()
		case "q":
			m.State = ViewStateDashboard
			return m, nil
		}
	}
	return m, nil
}

func (m *Model) restoreSelectedRecord() (tea.Model, tea.Cmd) {
	if len(m.RecoveryRecords) == 0 || m.RecoveryIdx < 0 || m.RecoveryIdx >= len(m.RecoveryRecords) {
		return m, nil
	}

	rec := m.RecoveryRecords[m.RecoveryIdx]
	if rec.Restored {
		return m, m.triggerStatus("Record is already restored.", true)
	}

	root := m.ScanPath
	recID := rec.ID
	origPath := rec.OriginalPath

	return m, func() tea.Msg {
		err := m.QuarantineManager.RestoreRecord(root, recID)
		return restoreCompletedMsg{ID: recID, Path: origPath, Err: err}
	}
}
