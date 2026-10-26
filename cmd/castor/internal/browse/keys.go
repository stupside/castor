package browse

import "charm.land/bubbles/v2/key"

// keyMap holds browse TUI bindings; j/k reserved for textinput on screenBrowse.
type keyMap struct {
	up, down         key.Binding
	pageUp, pageDown key.Binding
	tab, shiftTab    key.Binding
	enter, back      key.Binding
	play             key.Binding
	filter           key.Binding // display-only; list.Model owns the /-handling on drilldown
	genres           key.Binding
	sort             key.Binding
	media            key.Binding
	help             key.Binding
	quit             key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		up:       key.NewBinding(key.WithKeys("up"), key.WithHelp("↑", "up")),
		down:     key.NewBinding(key.WithKeys("down"), key.WithHelp("↓", "down")),
		pageUp:   key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "page up")),
		pageDown: key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdn", "page down")),
		tab:      key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next")),
		shiftTab: key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "prev")),
		enter:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("↵", "open")),
		back:     key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
		play:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("↵", "play")),
		filter:   key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		genres:   key.NewBinding(key.WithKeys("ctrl+g"), key.WithHelp("^g", "genres")),
		sort:     key.NewBinding(key.WithKeys("ctrl+s"), key.WithHelp("^s", "sort")),
		media:    key.NewBinding(key.WithKeys("ctrl+t"), key.WithHelp("^t", "movie/tv")),
		help:     key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		quit:     key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("^C", "quit")),
	}
}

// screenKeys adapts keyMap to bubbles' help.KeyMap, specialized to the current screen.
type screenKeys struct {
	k        keyMap
	s        screen
	discover bool
}

func (sk screenKeys) ShortHelp() []key.Binding {
	switch sk.s {
	case screenBrowse:
		b := []key.Binding{sk.k.up, sk.k.down, sk.k.tab, sk.k.genres, sk.k.enter}
		if sk.discover {
			b = append(b, sk.k.sort, sk.k.media)
		}
		return append(b, sk.k.back, sk.k.help, sk.k.quit)
	case screenDrilldown:
		return []key.Binding{sk.k.up, sk.k.down, sk.k.enter, sk.k.back, sk.k.filter, sk.k.help, sk.k.quit}
	}
	return nil
}

func (sk screenKeys) FullHelp() [][]key.Binding {
	nav := []key.Binding{sk.k.up, sk.k.down, sk.k.pageUp, sk.k.pageDown}
	switch sk.s {
	case screenBrowse:
		return [][]key.Binding{
			nav,
			{sk.k.tab, sk.k.shiftTab},
			{sk.k.genres, sk.k.sort, sk.k.media},
			{sk.k.enter, sk.k.back},
			{sk.k.help, sk.k.quit},
		}
	case screenDrilldown:
		return [][]key.Binding{
			nav,
			{sk.k.enter, sk.k.back, sk.k.filter},
			{sk.k.help, sk.k.quit},
		}
	}
	return nil
}
