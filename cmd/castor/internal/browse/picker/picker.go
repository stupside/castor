// Package picker asks the operator which device to cast to, among those the API server found.
package picker

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/stupside/castor/cmd/castor/internal/cast"
	"github.com/stupside/castor/cmd/castor/internal/palette"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

// Device is the device the operator picks, preselecting the one named defaultName; ok is false when they quit without one.
func Device(ctx context.Context, discover Discover, defaultName string) (picked *castorv1.Device, ok bool, err error) {
	final, err := tea.NewProgram(newModel(ctx, discover, defaultName), tea.WithContext(ctx)).Run()
	if err != nil {
		return nil, false, err
	}
	picked = final.(model).selected
	return picked, picked != nil, nil
}

type devicesDoneMsg struct {
	devices []*castorv1.Device
}

type model struct {
	ctx         context.Context
	discover    Discover
	defaultName string
	pal         palette.Palette
	list        list.Model
	help        help.Model
	spin        spinner.Model
	loading     bool
	selected    *castorv1.Device
	w           int
}

type item struct{ *castorv1.Device }

func (i item) Title() string { return i.GetName() }
func (i item) Description() string {
	return fmt.Sprintf("%s  %s", strings.ToUpper(cast.DeviceTypeName(i.GetType())), i.GetAddress())
}
func (i item) FilterValue() string { return i.GetName() }

func newModel(ctx context.Context, discover Discover, defaultName string) model {
	l := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(false)
	// The picker owns quitting, so a cancel is never mistaken for a selection.
	l.DisableQuitKeybindings()

	m := model{
		ctx:         ctx,
		discover:    discover,
		defaultName: defaultName,
		spin:        spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		list:        l,
		help:        help.New(),
		loading:     true,
	}
	m.restyle(true)
	return m
}

// restyle repaints for the terminal background; dark until the terminal reports it.
func (m *model) restyle(dark bool) {
	m.pal = palette.New(dark)
	m.spin.Style = lipgloss.NewStyle().Foreground(m.pal.Accent)
	m.list.SetDelegate(m.pal.Delegate())
	m.pal.StyleList(&m.list)
	m.list.Styles.NoItems = lipgloss.NewStyle().Foreground(m.pal.FgMuted).Padding(0, 2)
	m.help.ShortSeparator = " · "
	m.help.Styles.ShortKey = lipgloss.NewStyle().Foreground(m.pal.Accent).Bold(true)
	m.help.Styles.ShortDesc = lipgloss.NewStyle().Foreground(m.pal.FgMuted)
	m.help.Styles.ShortSeparator = lipgloss.NewStyle().Foreground(m.pal.Rule)
}

func (m model) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, m.spin.Tick, discoverDevicesCmd(m.ctx, m.discover))
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		m.restyle(msg.IsDark())
		return m, nil

	case tea.WindowSizeMsg:
		m.w = msg.Width
		m.list.SetSize(msg.Width-4, max(msg.Height-12, 5))
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case devicesDoneMsg:
		m.loading = false
		items := make([]list.Item, len(msg.devices))
		for i, d := range msg.devices {
			items[i] = item{d}
		}
		m.list.SetItems(items)
		if i := slices.IndexFunc(msg.devices, func(d *castorv1.Device) bool { return strings.EqualFold(d.GetName(), m.defaultName) }); i >= 0 {
			m.list.Select(i)
		}
		return m, nil

	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, keys.quit):
			return m, tea.Quit
		case key.Matches(msg, keys.enter):
			if it, ok := m.list.SelectedItem().(item); ok {
				m.selected = it.Device
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

func (m model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

func (m model) render() string {
	if m.loading {
		return m.spin.View() + lipgloss.NewStyle().Foreground(m.pal.FgMuted).Render(" Discovering devices…")
	}

	header := lipgloss.NewStyle().
		Background(m.pal.Bar).
		Foreground(m.pal.Accent).
		Bold(true).
		Width(m.w).
		Padding(0, 2).
		Render("castor  │  Select a device")

	body := m.list.View()

	cmdBar := lipgloss.NewStyle().
		Background(m.pal.Bar).
		Foreground(m.pal.FgPrimary).
		Width(m.w).
		Padding(0, 2).
		Render(m.help.ShortHelpView([]key.Binding{keys.nav, keys.enter, keys.quit}))

	return lipgloss.JoinVertical(lipgloss.Left, header, "", body, "", cmdBar)
}

type keyMap struct {
	nav   key.Binding // display-only; the list moves its own cursor
	enter key.Binding
	quit  key.Binding
}

var keys = keyMap{
	nav:   key.NewBinding(key.WithKeys("up", "down", "j", "k"), key.WithHelp("j/k", "nav")),
	enter: key.NewBinding(key.WithKeys("enter"), key.WithHelp("↵", "select")),
	quit:  key.NewBinding(key.WithKeys("ctrl+c", "q"), key.WithHelp("q", "quit")),
}

// discoverDevicesCmd prevents discovery sweeps from outliving app shutdown.
func discoverDevicesCmd(ctx context.Context, discover Discover) tea.Cmd {
	return func() tea.Msg {
		return devicesDoneMsg{devices: discover(ctx)}
	}
}

// Discover is one sweep for the devices on the network.
type Discover func(ctx context.Context) []*castorv1.Device
