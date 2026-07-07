package browse

import "github.com/charmbracelet/lipgloss"

var (
	accent      = lipgloss.AdaptiveColor{Light: "#6366F1", Dark: "#818CF8"}
	fgPrimary   = lipgloss.AdaptiveColor{Light: "#18181B", Dark: "#FAFAFA"}
	fgSecondary = lipgloss.AdaptiveColor{Light: "#52525B", Dark: "#D4D4D8"}
	fgMuted     = lipgloss.AdaptiveColor{Light: "#A1A1AA", Dark: "#52525B"}
	errorColor  = lipgloss.AdaptiveColor{Light: "#DC2626", Dark: "#FCA5A5"}
)

const (
	spInline = 2
	spGutter = 2
)

type styles struct {
	Title     lipgloss.Style
	TitleText lipgloss.Style
	MetaTitle lipgloss.Style
	Muted     lipgloss.Style
	Overview  lipgloss.Style
	Err       lipgloss.Style
}

func newStyles() styles {
	titleText := lipgloss.NewStyle().
		Bold(true).
		Foreground(accent)

	return styles{
		Title:     titleText.Padding(0, spInline),
		TitleText: titleText,
		MetaTitle: lipgloss.NewStyle().Foreground(fgSecondary),
		Muted:     lipgloss.NewStyle().Foreground(fgMuted),
		Overview: lipgloss.NewStyle().
			Foreground(fgPrimary).
			Width(posterCols),
		Err: lipgloss.NewStyle().
			Foreground(errorColor).
			Bold(true).
			Underline(true).
			Padding(0, spInline),
	}
}

func headerPad(s string) string {
	return lipgloss.NewStyle().Padding(0, spInline).Render(s)
}
