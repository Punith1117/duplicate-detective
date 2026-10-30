package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// Palette defines the dark terminal aesthetic with cyan accents.
var (
	ColorBgDark      = lipgloss.Color("#0B0F19") // Deep obsidian dark
	ColorSurface     = lipgloss.Color("#111827") // Surface dark
	ColorCardBg      = lipgloss.Color("#162032") // Card background
	ColorBorder      = lipgloss.Color("#1E293B") // Restrained subtle border
	ColorBorderFocus = lipgloss.Color("#06B6D4") // Cyan focus border

	ColorCyan       = lipgloss.Color("#06B6D4") // Main cyan accent
	ColorCyanBright = lipgloss.Color("#22D3EE") // Bright vibrant cyan
	ColorCyanGlow   = lipgloss.Color("#00F5FF") // High-contrast electric cyan
	ColorCyanMuted  = lipgloss.Color("#0E7490") // Deep restrained cyan

	ColorTextPrimary = lipgloss.Color("#F8FAFC") // High-contrast text
	ColorTextMuted   = lipgloss.Color("#94A3B8") // Secondary text
	ColorTextDim     = lipgloss.Color("#64748B") // Subtle / tertiary text

	ColorSuccess = lipgloss.Color("#10B981") // Keeper / verified green
	ColorWarning = lipgloss.Color("#F59E0B") // Attention / warning amber
	ColorDanger  = lipgloss.Color("#EF4444") // Deletion / error coral red
)

// Typography & Base Styles
var (
	HeaderTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(ColorCyanBright).
				Padding(0, 1)

	HeaderSubStyle = lipgloss.NewStyle().
			Foreground(ColorTextMuted).
			Italic(true)

	HeaderPathStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorCyanGlow)

	HeaderContainerStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(ColorCyanMuted).
				Background(ColorSurface).
				Padding(0, 1).
				MarginBottom(1)

	// Summary Metric Cards
	CardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorBorder).
			Background(ColorCardBg).
			Padding(0, 1).
			Width(24)

	CardActiveStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorBorderFocus).
			Background(ColorCardBg).
			Padding(0, 1).
			Width(24)

	CardLabelStyle = lipgloss.NewStyle().
			Foreground(ColorTextMuted).
			Bold(false)

	CardValueStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorCyanBright)

	CardSubValueStyle = lipgloss.NewStyle().
				Foreground(ColorTextDim)

	// Lists & Tables
	TableContainerStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(ColorBorder).
				Background(ColorSurface).
				Padding(0, 1)

	TableContainerActiveStyle = lipgloss.NewStyle().
					Border(lipgloss.RoundedBorder()).
					BorderForeground(ColorBorderFocus).
					Background(ColorSurface).
					Padding(0, 1)

	TableHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(ColorCyan).
				BorderBottom(true).
				BorderStyle(lipgloss.NormalBorder()).
				BorderForeground(ColorBorder).
				MarginBottom(0)

	TableRowStyle = lipgloss.NewStyle().
			Foreground(ColorTextPrimary).
			Padding(0, 0)

	TableRowSelectedStyle = lipgloss.NewStyle().
				Foreground(ColorCyanBright).
				Background(lipgloss.Color("#1E293B")).
				Bold(true)

	TableRowActiveStyle = lipgloss.NewStyle().
				Foreground(ColorTextPrimary).
				Background(lipgloss.Color("#172554")).
				Bold(true)

	// Badges
	KeeperBadge = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorBgDark).
			Background(ColorSuccess).
			Padding(0, 1).
			SetString("★ KEEPER")

	DuplicateBadge = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorTextMuted).
			Background(lipgloss.Color("#1E293B")).
			Padding(0, 1).
			SetString("DUPLICATE")

	SelectedBadge = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorBgDark).
			Background(ColorWarning).
			Padding(0, 1).
			SetString("TO CLEAN")

	ProtectedBadge = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorBgDark).
			Background(ColorCyan).
			Padding(0, 1).
			SetString("PROTECTED")

	// Modals & Overlays
	ModalOverlayStyle = lipgloss.NewStyle().
				Border(lipgloss.DoubleBorder()).
				BorderForeground(ColorCyan).
				Background(ColorSurface).
				Padding(1, 2)

	ModalTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorCyanBright).
			MarginBottom(1)

	ModalWarningStyle = lipgloss.NewStyle().
				Foreground(ColorWarning).
				Bold(true).
				MarginBottom(1)

	// Footer & Keyboard Shortcuts
	FooterContainerStyle = lipgloss.NewStyle().
				Background(ColorBgDark).
				MarginTop(1)

	KeyBadgeStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorCyanGlow).
			Background(lipgloss.Color("#1E293B")).
			Padding(0, 1)

	KeyDescStyle = lipgloss.NewStyle().
			Foreground(ColorTextMuted).
			MarginRight(1)

	StatusMessageStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(ColorCyanBright).
				Padding(0, 1)

	ErrorMessageStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(ColorDanger).
				Padding(0, 1)

	SuccessMessageStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(ColorSuccess).
				Padding(0, 1)

	// Input screen styles
	InputBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorCyan).
			Padding(0, 1).
			Width(64)
)

// FormatBytes converts a byte count into a human-readable string.
func FormatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	return fmt.Sprintf("%.2f %s", float64(bytes)/float64(div), units[exp])
}

// TruncatePath middle-truncates long file paths to fit the given max width.
func TruncatePath(path string, maxLen int) string {
	if len(path) <= maxLen || maxLen < 10 {
		return path
	}
	prefixLen := (maxLen - 3) / 2
	suffixLen := maxLen - 3 - prefixLen
	return path[:prefixLen] + "..." + path[len(path)-suffixLen:]
}
