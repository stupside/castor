package browse

import (
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/stupside/castor/cmd/castor/internal/cast"
)

type startPrompt struct {
	pending *Selection
	input   textinput.Model
	err     error
}

func newStartPrompt() startPrompt {
	in := textinput.New()
	in.Prompt = "❯ "
	in.Placeholder = "0:00"
	in.CharLimit = 12
	return startPrompt{input: in}
}

func (m model) choose(s *Selection) (tea.Model, tea.Cmd) {
	if !m.ask {
		m.sel = s
		return m, tea.Quit
	}
	m.start.pending, m.start.err = s, nil
	m.start.input.SetValue("")
	m.query.Blur()
	return m, m.start.input.Focus()
}

func (m model) updateStart(msg tea.Msg) (tea.Model, tea.Cmd) {
	if km, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case key.Matches(km, m.keys.play):
			d, err := cast.ParseStart(m.start.input.Value())
			if err != nil {
				m.start.err = err
				return m, nil
			}
			s := *m.start.pending
			s.Start = d
			m.sel = &s
			return m, tea.Quit
		case key.Matches(km, m.keys.back):
			m.start.pending, m.start.err = nil, nil
			m.start.input.Blur()
			return m, m.query.Focus()
		}
		m.start.err = nil
	}
	var cmd tea.Cmd
	m.start.input, cmd = m.start.input.Update(msg)
	return m, cmd
}

func (m model) viewStart() string {
	rows := []string{
		m.styles.titleText.Render(m.start.pending.Title),
		"",
		m.styles.metaTitle.Render("Start at (H:MM or 1h12m), or enter for the beginning"),
		m.start.input.View(),
	}
	if m.start.err != nil {
		rows = append(rows, m.styles.err.Render(m.start.err.Error()))
	} else {
		rows = append(rows, "")
	}
	rows = append(rows, m.help.ShortHelpView([]key.Binding{m.keys.play, m.keys.back, m.keys.quit}))
	return lipgloss.NewStyle().Padding(1, 2).Render(lipgloss.JoinVertical(lipgloss.Left, rows...))
}
