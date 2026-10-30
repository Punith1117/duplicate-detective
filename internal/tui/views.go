package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// View renders the TUI screen based on the model's current state.
func (m *Model) View() string {
	switch m.State {
	case ViewStateInput:
		return m.viewInput()
	case ViewStateScanning:
		return m.viewScanning()
	case ViewStateDashboard:
		return m.viewDashboard()
	case ViewStateConfirmClean:
		return m.viewConfirmClean()
	case ViewStateRecovery:
		return m.viewRecovery()
	default:
		return "Unknown view state"
	}
}

// viewInput renders the initial directory path prompt.
func (m *Model) viewInput() string {
	title := HeaderTitleStyle.Render("✦ DUPLICATE DETECTIVE ✦")
	subtitle := HeaderSubStyle.Render("Terminal File Deduplication & Safe Quarantine Engine")

	header := lipgloss.JoinVertical(lipgloss.Center, title, subtitle)

	inputVal := m.PathInput.Value
	cursorPos := m.PathInput.Cursor
	var renderedInput string
	if len(inputVal) == 0 {
		renderedInput = lipgloss.NewStyle().Foreground(ColorTextDim).Render(m.PathInput.Placeholder)
	} else {
		before := inputVal[:cursorPos]
		var at, after string
		if cursorPos < len(inputVal) {
			at = string(inputVal[cursorPos])
			after = inputVal[cursorPos+1:]
		}
		cursorChar := lipgloss.NewStyle().Reverse(true).Render(func() string {
			if at == "" {
				return " "
			}
			return at
		}())
		renderedInput = before + cursorChar + after
	}

	inputBox := InputBoxStyle.Render(renderedInput)
	promptLabel := lipgloss.NewStyle().Foreground(ColorCyan).Bold(true).Render("Directory to scan:")

	help := lipgloss.JoinHorizontal(lipgloss.Center,
		KeyBadgeStyle.Render("Enter"), KeyDescStyle.Render(" Scan directory  "),
		KeyBadgeStyle.Render("Esc"), KeyDescStyle.Render(" Cancel/Exit  "),
		KeyBadgeStyle.Render("Ctrl+C"), KeyDescStyle.Render(" Quit"),
	)

	var statusLine string
	if m.StatusMsg != "" {
		if m.StatusIsErr {
			statusLine = ErrorMessageStyle.Render("✖ " + m.StatusMsg)
		} else {
			statusLine = SuccessMessageStyle.Render("✔ " + m.StatusMsg)
		}
	}

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		"",
		promptLabel,
		inputBox,
		"",
		statusLine,
		"",
		help,
	)

	return lipgloss.Place(m.Width, m.Height, lipgloss.Center, lipgloss.Center, content)
}

// viewScanning renders the active scan progress indicator.
func (m *Model) viewScanning() string {
	dots := strings.Repeat(".", m.ScanningDots)
	for len(dots) < 3 {
		dots += " "
	}

	spinner := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorCyanGlow).
		Render("⠋⠙⠹⠸⠼⠴⠦⠧"[m.ScanningDots%8 : m.ScanningDots%8+3])

	scanText := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorCyanBright).
		Render(fmt.Sprintf("%s Scanning directory tree%s", spinner, dots))

	pathText := lipgloss.NewStyle().
		Foreground(ColorTextMuted).
		Render("Target: " + m.ScanPath)

	hashNote := lipgloss.NewStyle().
		Foreground(ColorTextDim).
		Italic(true).
		Render("Analyzing file sizes & computing streaming SHA-256 hashes...")

	cancelHelp := lipgloss.JoinHorizontal(lipgloss.Center,
		KeyBadgeStyle.Render("Esc"), KeyDescStyle.Render(" Cancel scan"),
	)

	content := lipgloss.JoinVertical(
		lipgloss.Center,
		HeaderTitleStyle.Render("✦ DUPLICATE DETECTIVE ✦"),
		"",
		scanText,
		pathText,
		"",
		hashNote,
		"",
		cancelHelp,
	)

	return lipgloss.Place(m.Width, m.Height, lipgloss.Center, lipgloss.Center, content)
}

// viewDashboard renders the primary terminal dashboard.
func (m *Model) viewDashboard() string {
	header := m.renderHeader()
	cards := m.renderCards()
	panes := m.renderMasterDetailPanes()
	footer := m.renderFooter()

	dashboard := lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		cards,
		panes,
		footer,
	)

	return dashboard
}

func (m *Model) renderHeader() string {
	title := HeaderTitleStyle.Render("✦ DUPLICATE DETECTIVE")
	pathDisplay := HeaderPathStyle.Render(TruncatePath(m.ScanPath, 50))

	var summaryInfo string
	if m.ScanResult != nil {
		summaryInfo = lipgloss.NewStyle().Foreground(ColorTextMuted).Render(
			fmt.Sprintf("Scanned: %d files (%s)", m.ScanResult.TotalFiles, FormatBytes(m.ScanResult.TotalSize)),
		)
	}

	left := lipgloss.JoinHorizontal(lipgloss.Center, title, lipgloss.NewStyle().Foreground(ColorCyanMuted).Render(" │ "), pathDisplay)
	right := summaryInfo

	spacerWidth := m.Width - lipgloss.Width(left) - lipgloss.Width(right) - 4
	if spacerWidth < 1 {
		spacerWidth = 1
	}
	spacer := strings.Repeat(" ", spacerWidth)

	headerRow := lipgloss.JoinHorizontal(lipgloss.Center, left, spacer, right)
	return HeaderContainerStyle.Width(m.Width - 2).Render(headerRow)
}

func (m *Model) renderCards() string {
	groupsCount := 0
	redundantFiles := 0
	var wastedSpace int64

	if m.ScanResult != nil {
		groupsCount = len(m.ScanResult.Duplicates)
		redundantFiles = m.ScanResult.TotalDuplicateFiles()
		wastedSpace = m.ScanResult.TotalWastedSpace()
	}

	selectedCount, selectedBytes := m.CalculateSelectedSavings()

	cardW := (m.Width - 10) / 4
	if cardW < 18 {
		cardW = 18
	}

	c1 := m.makeCard("Duplicate Groups", fmt.Sprintf("%d", groupsCount), "Unique hash sets", cardW, false)
	c2 := m.makeCard("Redundant Files", fmt.Sprintf("%d", redundantFiles), "Eligible copies", cardW, false)
	c3 := m.makeCard("Potential Savings", FormatBytes(wastedSpace), "Total reclaimable", cardW, false)

	cleanActive := selectedCount > 0
	cleanVal := fmt.Sprintf("%d files", selectedCount)
	cleanSub := FormatBytes(selectedBytes) + " to free"
	c4 := m.makeCard("Selected to Clean", cleanVal, cleanSub, cardW, cleanActive)

	return lipgloss.JoinHorizontal(lipgloss.Top, c1, " ", c2, " ", c3, " ", c4)
}

func (m *Model) makeCard(label, val, sub string, width int, active bool) string {
	style := CardStyle.Width(width)
	if active {
		style = CardActiveStyle.Width(width)
	}

	l := CardLabelStyle.Render(label)
	v := CardValueStyle.Render(val)
	s := CardSubValueStyle.Render(sub)

	return style.Render(lipgloss.JoinVertical(lipgloss.Left, l, v, s))
}

func (m *Model) renderMasterDetailPanes() string {
	availableHeight := m.Height - 16
	if availableHeight < 8 {
		availableHeight = 8
	}

	// Calculate widths
	totalWidth := m.Width - 4
	leftWidth := (totalWidth * 42) / 100
	if leftWidth < 36 {
		leftWidth = 36
	}
	rightWidth := totalWidth - leftWidth - 2
	if rightWidth < 40 {
		rightWidth = 40
	}

	leftPane := m.renderGroupsPane(leftWidth, availableHeight)
	rightPane := m.renderFilesPane(rightWidth, availableHeight)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, " ", rightPane)
}

func (m *Model) renderGroupsPane(width, height int) string {
	style := TableContainerStyle.Width(width).Height(height)
	if m.ActivePane == PaneGroups {
		style = TableContainerActiveStyle.Width(width).Height(height)
	}

	paneTitle := lipgloss.NewStyle().Bold(true).Foreground(ColorCyanBright).Render("DUPLICATE GROUPS")
	if m.ActivePane == PaneGroups {
		paneTitle += lipgloss.NewStyle().Foreground(ColorCyanGlow).Render(" ◀ [ACTIVE]")
	}

	groups := m.currentGroups()
	if len(groups) == 0 {
		emptyMsg := lipgloss.NewStyle().Foreground(ColorTextDim).Italic(true).Render("No duplicate groups found.")
		return style.Render(lipgloss.JoinVertical(lipgloss.Left, paneTitle, "", emptyMsg))
	}

	// Table header
	hdr := TableHeaderStyle.Width(width - 4).Render(
		fmt.Sprintf(" %-4s %-12s %-6s %-10s %-10s", "#", "HASH", "FILES", "UNIT SIZE", "WASTED"),
	)

	var rows []string
	rows = append(rows, paneTitle, hdr)

	visibleRows := height - 4
	if visibleRows < 1 {
		visibleRows = 1
	}

	start := m.GroupScroll
	end := start + visibleRows
	if end > len(groups) {
		end = len(groups)
	}

	for i := start; i < end; i++ {
		g := groups[i]
		shortHash := g.Hash
		if len(shortHash) > 10 {
			shortHash = shortHash[:10]
		}

		// Count selected in this group
		selectedInGroup := 0
		keeper := m.Keepers[g.Hash]
		for _, f := range g.Files {
			if f.Path != keeper && m.SelectedFiles[f.Path] {
				selectedInGroup++
			}
		}

		selIndicator := " "
		if selectedInGroup > 0 {
			selIndicator = fmt.Sprintf("✓%d", selectedInGroup)
		}

		line := fmt.Sprintf("%-4d %-12s %-6d %-10s %-10s %s",
			i+1,
			shortHash,
			len(g.Files),
			FormatBytes(g.Size),
			FormatBytes(g.WastedSpace()),
			selIndicator,
		)

		if i == m.SelectedGroupIdx {
			if m.ActivePane == PaneGroups {
				rows = append(rows, TableRowActiveStyle.Width(width-4).Render("▶ "+line))
			} else {
				rows = append(rows, TableRowSelectedStyle.Width(width-4).Render("▷ "+line))
			}
		} else {
			rows = append(rows, TableRowStyle.Width(width-4).Render("  "+line))
		}
	}

	return style.Render(lipgloss.JoinVertical(lipgloss.Left, rows...))
}

func (m *Model) renderFilesPane(width, height int) string {
	style := TableContainerStyle.Width(width).Height(height)
	if m.ActivePane == PaneFiles {
		style = TableContainerActiveStyle.Width(width).Height(height)
	}

	group := m.currentGroup()
	if group == nil {
		emptyMsg := lipgloss.NewStyle().Foreground(ColorTextDim).Italic(true).Render("Select a duplicate group to inspect files.")
		return style.Render(emptyMsg)
	}

	keeperPath := m.Keepers[group.Hash]
	paneTitle := lipgloss.NewStyle().Bold(true).Foreground(ColorCyanBright).Render(
		fmt.Sprintf("GROUP %d INSPECTION (Hash: %s)", m.SelectedGroupIdx+1, group.Hash[:12]),
	)
	if m.ActivePane == PaneFiles {
		paneTitle += lipgloss.NewStyle().Foreground(ColorCyanGlow).Render(" ◀ [ACTIVE]")
	}

	files := group.Files
	visibleRows := height - 8
	if visibleRows < 1 {
		visibleRows = 1
	}

	start := m.FileScroll
	end := start + visibleRows
	if end > len(files) {
		end = len(files)
	}

	var rows []string
	rows = append(rows, paneTitle)

	// Subheader showing total files & unit size
	subHdr := lipgloss.NewStyle().Foreground(ColorTextMuted).Render(
		fmt.Sprintf("%d duplicate files • %s unit size • Modified dates & paths shown below:", len(files), FormatBytes(group.Size)),
	)
	rows = append(rows, subHdr, "")

	for i := start; i < end; i++ {
		f := files[i]
		isKeeper := f.Path == keeperPath
		isSelected := m.SelectedFiles[f.Path]

		var badge string
		var check string
		if isKeeper {
			badge = KeeperBadge.Render()
			check = ProtectedBadge.Render()
		} else if isSelected {
			badge = DuplicateBadge.Render()
			check = SelectedBadge.Render()
		} else {
			badge = DuplicateBadge.Render()
			check = lipgloss.NewStyle().Foreground(ColorTextDim).Render("[ ] UNSELECTED")
		}

		dispPath := TruncatePath(f.Path, width-36)
		modTime := f.ModTime.Format("2006-01-02 15:04")

		line := fmt.Sprintf("%s %s  %s  (%s)", check, badge, dispPath, modTime)

		if i == m.SelectedFileIdx {
			if m.ActivePane == PaneFiles {
				rows = append(rows, TableRowActiveStyle.Width(width-4).Render("▶ "+line))
			} else {
				rows = append(rows, TableRowSelectedStyle.Width(width-4).Render("▷ "+line))
			}
		} else {
			rows = append(rows, TableRowStyle.Width(width-4).Render("  "+line))
		}
	}

	// Inspector detail card for the currently highlighted file
	currFile := m.currentFile()
	if currFile != nil {
		isKeeper := currFile.Path == keeperPath
		var detailStatus string
		if isKeeper {
			detailStatus = lipgloss.NewStyle().Foreground(ColorSuccess).Bold(true).Render("★ RECOMMENDED KEEPER (PROTECTED FROM CLEANUP)")
		} else if m.SelectedFiles[currFile.Path] {
			detailStatus = lipgloss.NewStyle().Foreground(ColorWarning).Bold(true).Render("⚠ SELECTED FOR QUARANTINE CLEANUP")
		} else {
			detailStatus = lipgloss.NewStyle().Foreground(ColorTextMuted).Render("DUPLICATE COPY (ELIGIBLE FOR SELECTION)")
		}

		fullPathLabel := lipgloss.NewStyle().Foreground(ColorCyanBright).Render("Path: ") +
			lipgloss.NewStyle().Foreground(ColorTextPrimary).Render(currFile.Path)

		var reasonLabel string
		if isKeeper {
			reason := m.Planner.ExplainRecommendation(*group)
			if reason != "" {
				reasonLabel = lipgloss.NewStyle().Foreground(ColorTextMuted).Italic(true).Render("Recommendation: " + reason)
			}
		} else {
			reasonLabel = lipgloss.NewStyle().Foreground(ColorTextDim).Italic(true).Render("Recommendation: Duplicate copy of keeper " + TruncatePath(keeperPath, 45))
		}

		detailContent := []string{detailStatus, fullPathLabel}
		if reasonLabel != "" {
			detailContent = append(detailContent, reasonLabel)
		}

		detailBox := lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(ColorBorder).
			Background(ColorCardBg).
			Padding(0, 1).
			Width(width - 4).
			Render(lipgloss.JoinVertical(lipgloss.Left, detailContent...))

		rows = append(rows, "", detailBox)
	}

	return style.Render(lipgloss.JoinVertical(lipgloss.Left, rows...))
}

func (m *Model) renderFooter() string {
	// Status / Notification Line
	var statusLine string
	if m.StatusMsg != "" {
		if m.StatusIsErr {
			statusLine = ErrorMessageStyle.Render("✖ " + m.StatusMsg)
		} else {
			statusLine = SuccessMessageStyle.Render("✔ " + m.StatusMsg)
		}
	} else {
		statusLine = lipgloss.NewStyle().Foreground(ColorTextDim).Render("Ready. Keepers are protected automatically.")
	}

	// Keyboard Shortcuts Line
	shortcuts := lipgloss.JoinHorizontal(lipgloss.Center,
		KeyBadgeStyle.Render("Enter"), KeyDescStyle.Render(" Select/open"),
		KeyBadgeStyle.Render("↑/↓/←/→"), KeyDescStyle.Render(" Navigate"),
		KeyBadgeStyle.Render("Space"), KeyDescStyle.Render(" Toggle selection"),
		KeyBadgeStyle.Render("A"), KeyDescStyle.Render(" Select eligible"),
		KeyBadgeStyle.Render("C"), KeyDescStyle.Render(" Clean"),
		KeyBadgeStyle.Render("E"), KeyDescStyle.Render(" Export CSV"),
		KeyBadgeStyle.Render("R"), KeyDescStyle.Render(" Recovery history"),
		KeyBadgeStyle.Render("U"), KeyDescStyle.Render(" Restore"),
		KeyBadgeStyle.Render("Esc"), KeyDescStyle.Render(" Back"),
		KeyBadgeStyle.Render("Q"), KeyDescStyle.Render(" Quit"),
	)

	return FooterContainerStyle.Width(m.Width - 2).Render(
		lipgloss.JoinVertical(lipgloss.Left, statusLine, shortcuts),
	)
}

// viewConfirmClean renders the modal confirmation dialog for quarantine.
func (m *Model) viewConfirmClean() string {
	count, bytes := m.CalculateSelectedSavings()

	title := ModalTitleStyle.Render("⚠ CONFIRM QUARANTINE CLEANUP")
	warning := ModalWarningStyle.Render(
		fmt.Sprintf("You are about to quarantine %d duplicate file(s) freeing %s.", count, FormatBytes(bytes)),
	)
	note := lipgloss.NewStyle().Foreground(ColorTextMuted).Render(
		"Files will be safely relocated to the quarantine vault with full restore capability.\nAll recommended keeper files remain untouched and protected.",
	)

	prompt := lipgloss.JoinHorizontal(lipgloss.Center,
		KeyBadgeStyle.Render("Y / Enter"), KeyDescStyle.Render(" Confirm Quarantine  "),
		KeyBadgeStyle.Render("N / Esc"), KeyDescStyle.Render(" Cancel & Return"),
	)

	box := ModalOverlayStyle.Width(68).Render(
		lipgloss.JoinVertical(lipgloss.Left, title, warning, "", note, "", prompt),
	)

	return lipgloss.Place(m.Width, m.Height, lipgloss.Center, lipgloss.Center, box)
}

// viewRecovery renders the quarantine recovery and restore action view.
func (m *Model) viewRecovery() string {
	title := ModalTitleStyle.Render("✦ QUARANTINE RECOVERY & RESTORE HISTORY ✦")
	sub := lipgloss.NewStyle().Foreground(ColorTextMuted).Render(
		"Review quarantined files and restore them to their original filesystem locations.",
	)

	var warnBox string
	if len(m.ReconciliationWarnings) > 0 {
		var warnItems []string
		warnItems = append(warnItems, lipgloss.NewStyle().Foreground(ColorWarning).Bold(true).Render(
			fmt.Sprintf("⚠ RECOVERY WARNINGS / INCONSISTENCIES (%d):", len(m.ReconciliationWarnings)),
		))
		for _, w := range m.ReconciliationWarnings {
			warnItems = append(warnItems, lipgloss.NewStyle().Foreground(ColorWarning).Render("  • "+w))
		}
		warnBox = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(ColorWarning).
			Background(ColorCardBg).
			Padding(0, 1).
			MarginBottom(1).
			Render(lipgloss.JoinVertical(lipgloss.Left, warnItems...))
	}

	records := m.RecoveryRecords
	var body string

	if len(records) == 0 {
		body = lipgloss.NewStyle().Foreground(ColorTextDim).Italic(true).Render(
			"\nNo files have been quarantined yet in this directory vault.\n",
		)
	} else {
		var rows []string
		hdr := TableHeaderStyle.Render(
			fmt.Sprintf(" %-6s %-18s %-10s %-12s %s", "STATUS", "DATE", "SIZE", "ID", "ORIGINAL PATH"),
		)
		rows = append(rows, hdr)

		visibleRows := m.Height - 14
		if visibleRows < 4 {
			visibleRows = 4
		}

		start := m.RecoveryScroll
		end := start + visibleRows
		if end > len(records) {
			end = len(records)
		}

		for i := start; i < end; i++ {
			rec := records[i]
			statusBadge := lipgloss.NewStyle().Foreground(ColorWarning).Render("[QUAR]")
			if rec.Restored {
				statusBadge = lipgloss.NewStyle().Foreground(ColorSuccess).Render("[REST]")
			}

			dateStr := rec.QuarantinedAt.Format("2006-01-02 15:04")
			dispPath := TruncatePath(rec.OriginalPath, 45)

			line := fmt.Sprintf("%s %-18s %-10s %-12s %s",
				statusBadge,
				dateStr,
				FormatBytes(rec.Size),
				rec.ID,
				dispPath,
			)

			if i == m.RecoveryIdx {
				rows = append(rows, TableRowActiveStyle.Render("▶ "+line))
			} else {
				rows = append(rows, TableRowStyle.Render("  "+line))
			}
		}

		body = lipgloss.JoinVertical(lipgloss.Left, rows...)
	}

	var statusLine string
	if m.StatusMsg != "" {
		if m.StatusIsErr {
			statusLine = ErrorMessageStyle.Render("✖ " + m.StatusMsg)
		} else {
			statusLine = SuccessMessageStyle.Render("✔ " + m.StatusMsg)
		}
	}

	help := lipgloss.JoinHorizontal(lipgloss.Center,
		KeyBadgeStyle.Render("U / Enter"), KeyDescStyle.Render(" Restore selected file  "),
		KeyBadgeStyle.Render("↑/↓"), KeyDescStyle.Render(" Navigate  "),
		KeyBadgeStyle.Render("Esc"), KeyDescStyle.Render(" Return to dashboard"),
	)

	var contentItems []string
	contentItems = append(contentItems, title, sub, "")
	if warnBox != "" {
		contentItems = append(contentItems, warnBox)
	}
	contentItems = append(contentItems, body, "", statusLine, "", help)

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		contentItems...,
	)

	box := ModalOverlayStyle.Width(m.Width - 8).Render(content)
	return lipgloss.Place(m.Width, m.Height, lipgloss.Center, lipgloss.Center, box)
}
