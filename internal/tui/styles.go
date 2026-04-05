package tui

import "github.com/charmbracelet/lipgloss"

var (
	// Colors
	colorPrimary   = lipgloss.Color("#0F766E") // teal
	colorSecondary = lipgloss.Color("#0284C7") // blue
	colorSuccess   = lipgloss.Color("#10B981") // green
	colorError     = lipgloss.Color("#EF4444") // red
	colorWarning   = lipgloss.Color("#D97706") // amber
	colorMuted     = lipgloss.Color("#64748B") // slate
	colorBorder    = lipgloss.Color("#334155") // slate dark
	colorUser      = lipgloss.Color("#2563EB") // blue
	colorAssistant = lipgloss.Color("#059669") // emerald

	// Header
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary).
			Padding(0, 1)

	sessionStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorSecondary)

	// Messages
	userLabelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorUser)

	assistantLabelStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorAssistant)

	// Tool cards
	toolBorderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(0, 1).
			MarginLeft(2)

	toolNameStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorSecondary)

	toolStatusRunning = lipgloss.NewStyle().
				Foreground(colorWarning)

	toolStatusDone = lipgloss.NewStyle().
			Foreground(colorSuccess)

	toolStatusError = lipgloss.NewStyle().
			Foreground(colorError)

	// Input
	inputBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorBorder).
				Padding(0, 1)

	inputPromptStyle = lipgloss.NewStyle().
				Foreground(colorPrimary).
				Bold(true)

	inputLabelStyle = lipgloss.NewStyle().
			Foreground(colorPrimary).
			Bold(true)

	// Permission dialog
	permBorderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorWarning).
			Padding(0, 1).
			MarginLeft(2)

	permLabelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorWarning)

	// General
	mutedStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	errorStyle = lipgloss.NewStyle().
			Foreground(colorError)

	emptyStateStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(1, 2)

	emptyTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary)

	systemHintStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorSecondary)
)
