package browse

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/stupside/castor/internal/device"
)

func PickDevice(ctx context.Context, timeout time.Duration, defaultName string) (device.Info, error) {
	m := newPickerModel(timeout, defaultName)
	final, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	if err != nil {
		return device.Info{}, err
	}
	if fm, ok := final.(pickerModel); ok {
		return fm.selected, fm.err
	}
	return device.Info{}, nil
}

type discoverDoneMsg struct {
	devices []device.Info
	err     error
}

var (
	pAccent  = lipgloss.AdaptiveColor{Light: "#6366F1", Dark: "#818CF8"}
	pPrimary = lipgloss.AdaptiveColor{Light: "#18181B", Dark: "#FAFAFA"}
	pSec     = lipgloss.AdaptiveColor{Light: "#52525B", Dark: "#D4D4D8"}
	pMuted   = lipgloss.AdaptiveColor{Light: "#A1A1AA", Dark: "#52525B"}
	pDim     = lipgloss.AdaptiveColor{Light: "#D4D4D8", Dark: "#3F3F46"}
	pRed     = lipgloss.AdaptiveColor{Light: "#DC2626", Dark: "#FCA5A5"}
)

type pickerModel struct {
	timeout     time.Duration
	defaultName string
	list        list.Model
	spin        spinner.Model
	loading     bool
	err         error
	selected    device.Info

	showQuitModal bool
	w             int
	h             int
}

type pickerItem device.Info

func (i pickerItem) Title() string       { return i.Name }
func (i pickerItem) Description() string { return fmt.Sprintf("%s  %s", strings.ToUpper(string(i.Type)), i.Address) }
func (i pickerItem) FilterValue() string { return i.Name }

func newPickerModel(timeout time.Duration, defaultName string) pickerModel {
	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = lipgloss.NewStyle().Foreground(pAccent)

	delegate := list.NewDefaultDelegate()
	delegate.Styles.NormalTitle = delegate.Styles.NormalTitle.Foreground(pPrimary)
	delegate.Styles.NormalDesc = delegate.Styles.NormalDesc.Foreground(pMuted)
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
		Foreground(pAccent).
		BorderForeground(pAccent).
		Bold(true)
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.
		Foreground(pSec).
		BorderForeground(pAccent)

	l := list.New(nil, delegate, 0, 0)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(false)
	l.Styles.NoItems = lipgloss.NewStyle().Foreground(pMuted).Padding(0, 2)

	return pickerModel{
		timeout:     timeout,
		defaultName: defaultName,
		spin:        sp,
		list:        l,
		loading:     true,
	}
}

func (m pickerModel) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, discoverCmd(m.timeout))
}

func (m pickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.list.SetSize(msg.Width-4, max(msg.Height-12, 5))
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case discoverDoneMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		items := make([]list.Item, len(msg.devices))
		for i, d := range msg.devices {
			items[i] = pickerItem(d)
		}
		m.list.SetItems(items)
		if m.defaultName != "" {
			for i, d := range msg.devices {
				if d.Name == m.defaultName {
					m.list.Select(i)
					break
				}
			}
		}
		return m, nil

	case tea.KeyMsg:
		if m.showQuitModal {
			switch {
			case key.Matches(msg, pickKeys.Enter), key.Matches(msg, pickKeys.Quit):
				m.err = fmt.Errorf("cancelled")
				return m, tea.Quit
			case key.Matches(msg, pickKeys.Back):
				m.showQuitModal = false
				return m, nil
			}
			return m, nil
		}
		switch {
		case key.Matches(msg, pickKeys.Quit):
			m.showQuitModal = true
			return m, nil
		case key.Matches(msg, pickKeys.Enter):
			if it, ok := m.list.SelectedItem().(pickerItem); ok {
				m.selected = device.Info(it)
				return m, tea.Quit
			}
			return m, nil
		default:
			var cmd tea.Cmd
			m.list, cmd = m.list.Update(msg)
			return m, cmd
		}
	}

	return m, nil
}

func (m pickerModel) View() string {
	if m.loading {
		return m.spin.View() + lipgloss.NewStyle().Foreground(pMuted).Render(" Discovering devices…")
	}
	if m.err != nil && m.selected == (device.Info{}) {
		return lipgloss.NewStyle().Foreground(pRed).Bold(true).Render("error: " + m.err.Error())
	}

	if m.showQuitModal {
		return m.renderModal()
	}

	header := lipgloss.NewStyle().
		Background(lipgloss.AdaptiveColor{Light: "#F4F4F5", Dark: "#27272A"}).
		Foreground(pAccent).
		Bold(true).
		Width(m.w).
		Padding(0, 2).
		Render("castor  │  Select a device")

	body := m.list.View()

	cmds := []string{
		lipgloss.NewStyle().Foreground(pAccent).Bold(true).Render("j/k") + " " + lipgloss.NewStyle().Foreground(pMuted).Render("nav"),
		lipgloss.NewStyle().Foreground(pAccent).Bold(true).Render("↵") + " " + lipgloss.NewStyle().Foreground(pMuted).Render("select"),
		lipgloss.NewStyle().Foreground(pAccent).Bold(true).Render("q") + " " + lipgloss.NewStyle().Foreground(pMuted).Render("quit"),
	}
	cmdBar := lipgloss.NewStyle().
		Background(lipgloss.AdaptiveColor{Light: "#E4E4E7", Dark: "#18181B"}).
		Foreground(pPrimary).
		Width(m.w).
		Padding(0, 2).
		Render(strings.Join(cmds, lipgloss.NewStyle().Foreground(pDim).Render(" · ")))

	return lipgloss.JoinVertical(lipgloss.Left, header, "", body, "", cmdBar)
}

func (m pickerModel) renderModal() string {
	modalW := 44
	content := lipgloss.JoinVertical(lipgloss.Center,
		lipgloss.NewStyle().Bold(true).Foreground(pAccent).Render("Quit castor?"),
		"",
		lipgloss.JoinHorizontal(lipgloss.Center,
			lipgloss.NewStyle().Foreground(pRed).Bold(true).Render("[ Yes ]"),
			lipgloss.NewStyle().Foreground(pMuted).Render("  "),
			lipgloss.NewStyle().Foreground(pMuted).Render("[ No ]"),
		),
		"",
		lipgloss.NewStyle().Foreground(pDim).Render("↵ / q to quit  •  esc to go back"),
	)
	box := lipgloss.NewStyle().
		Width(modalW).
		Height(9).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(pAccent).
		Render(content)
	return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, box)
}

type pickerKeyMap struct {
	Enter key.Binding
	Up    key.Binding
	Down  key.Binding
	Quit  key.Binding
	Back  key.Binding
}

var pickKeys = pickerKeyMap{
	Enter: key.NewBinding(key.WithKeys("enter"), key.WithHelp("↵", "select")),
	Up:    key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
	Down:  key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
	Quit:  key.NewBinding(key.WithKeys("ctrl+c", "q"), key.WithHelp("q", "quit")),
	Back:  key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
}

func discoverCmd(timeout time.Duration) tea.Cmd {
	return func() tea.Msg {
		devices, err := device.Discover(context.Background(), timeout)
		return discoverDoneMsg{devices: devices, err: err}
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
