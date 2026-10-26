package browse

import (
	"context"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/stupside/castor/cmd/castor/internal/browse/tmdb"
	"github.com/stupside/castor/cmd/castor/internal/palette"
)

type screen int

const (
	screenBrowse screen = iota
	screenDrilldown
)

type model struct {
	ctx    context.Context
	client Catalog

	styles styles
	keys   keyMap
	help   help.Model
	spin   spinner.Model

	scr screen

	tab        tabID
	mode       browseMode
	query      textinput.Model
	queryTok   int // monotonic; only the latest debounce tick fires
	results    list.Model
	topsCache  [tabCount][]list.Item
	topsCursor [tabCount]int
	disc       discoverState

	inspector inspector
	genres    genrePicker
	drill     drilldown

	loading bool
	err     error

	sel  *Selection
	ask  bool
	w, h int

	badge string

	start startPrompt
}

func newModel(ctx context.Context, client Catalog, badge string) model {
	q := textinput.New()
	q.Placeholder = "Type to search TMDB…"
	q.Prompt = "❯ "
	q.CharLimit = 128
	q.Focus()

	results := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	results.SetShowTitle(false)
	results.SetShowStatusBar(false)
	results.SetShowHelp(false)
	results.SetFilteringEnabled(false) // the textinput owns filtering on browse

	m := model{
		ctx:       ctx,
		client:    client,
		keys:      defaultKeys(),
		help:      help.New(),
		spin:      spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		scr:       screenBrowse,
		tab:       tabTrending,
		mode:      modeCurated,
		query:     q,
		results:   results,
		disc:      discoverState{sort: tmdb.SortPopularity},
		inspector: newInspector(ctx, client),
		genres:    newGenrePicker(),
		drill:     newDrilldown(ctx, client),
		loading:   true,
		badge:     badge,
		start:     newStartPrompt(),
	}
	m.restyle(true)
	return m
}

// restyle repaints for the terminal background; dark until the terminal reports it.
func (m *model) restyle(dark bool) {
	p := palette.New(dark)
	m.styles = newStyles(p)
	m.help.Styles = newHelpStyles(p)
	m.spin.Style = lipgloss.NewStyle().Foreground(p.Accent)
	m.query.SetStyles(newQueryStyles(p))
	m.start.input.SetStyles(newQueryStyles(p))
	m.results.SetDelegate(p.Delegate())
	p.StyleList(&m.results)
	m.inspector.styles = m.styles
	m.genres.restyle(p, m.styles)
	m.drill.restyle(p)
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		tea.RequestBackgroundColor,
		m.spin.Tick,
		textinput.Blink,
		loadTopCmd(m.ctx, m.client, m.tab),
		loadGenresCmd(m.ctx, m.client),
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		m.restyle(msg.IsDark())
		return m, nil

	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.help.SetWidth(msg.Width)
		m.resize()
		if m.genres.draft != nil {
			m.genres.resize(m.w, m.h)
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		if !m.spinning() {
			return m, nil // restarted by whatever loads next
		}
		return m, cmd

	case tea.KeyPressMsg:
		if key.Matches(msg, m.keys.quit) {
			return m, tea.Quit
		}
		if m.start.pending != nil {
			return m.updateStart(msg)
		}
		if m.genres.draft != nil {
			cmd, action := m.genres.update(msg, m.keys, m.w, m.h)
			if action == genreApplied {
				cmd = m.enterDiscover()
			}
			return m, cmd
		}
		if key.Matches(msg, m.keys.help) && !m.drilldownFiltering() {
			m.help.ShowAll = !m.help.ShowAll
			m.resize()
			return m, nil
		}

	case genresLoadedMsg:
		m.genres.err = msg.err
		if msg.err == nil {
			m.genres.setCatalog(msg.cat, m.w, m.h)
		}
		return m, nil

	case topsLoadedMsg:
		return m.onTopsLoaded(msg)

	case searchTickMsg:
		return m.onSearchTick(msg)

	case searchDoneMsg:
		if msg.tok != m.queryTok {
			return m, nil
		}
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.results.SetItems(toResultItems(msg.res))
		m.results.Select(0)
		return m, m.inspector.hover()

	case discoverDoneMsg:
		return m, m.onDiscoverDone(msg)

	case posterReadyMsg, detailsReadyMsg, hoverSettleMsg:
		cmd := m.inspector.update(msg, m.selectedResult())
		return m, cmd

	case tvDoneMsg:
		if msg.tok != m.drill.tok {
			return m, nil
		}
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.drill.showSeasons(msg.tv)
		m.goTo(screenDrilldown)
		return m, nil

	case seasonDoneMsg:
		if msg.tok != m.drill.tok {
			return m, nil
		}
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.drill.showEpisodes(msg.sd)
		return m, nil
	}

	if m.start.pending != nil {
		return m.updateStart(msg)
	}
	switch m.scr {
	case screenBrowse:
		return m.updateBrowse(msg)
	case screenDrilldown:
		return m.updateDrilldown(msg)
	}
	return m, nil
}

func (m model) updateBrowse(msg tea.Msg) (tea.Model, tea.Cmd) {
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m.forwardToQuery(msg)
	}
	switch {
	case key.Matches(km, m.keys.genres):
		m.genres.open(m.w, m.h)
		if m.genres.err != nil {
			m.genres.err = nil
			return m, tea.Batch(loadGenresCmd(m.ctx, m.client), m.spin.Tick)
		}
		if !m.genres.loaded {
			return m, m.spin.Tick
		}
		return m, nil
	case key.Matches(km, m.keys.sort):
		if m.mode == modeDiscover {
			return m, m.cycleSort()
		}
		return m, nil
	case key.Matches(km, m.keys.media):
		if m.mode == modeDiscover {
			m.genres.applied = m.genres.applied.otherMedia()
			return m, m.enterDiscover()
		}
		return m, nil
	case key.Matches(km, m.keys.tab):
		if m.query.Value() == "" {
			return m, m.cycleTab(1)
		}
		return m, nil
	case key.Matches(km, m.keys.shiftTab):
		if m.query.Value() == "" {
			return m, m.cycleTab(-1)
		}
		return m, nil
	case key.Matches(km, m.keys.enter):
		if r := m.selectedResult(); r != nil {
			return m.pickResult(*r)
		}
		return m, nil
	case key.Matches(km, m.keys.back):
		return m.onBrowseBack()
	case key.Matches(km, m.keys.up),
		key.Matches(km, m.keys.down),
		key.Matches(km, m.keys.pageUp),
		key.Matches(km, m.keys.pageDown):
		return m.delegateResults(msg)
	}
	return m.forwardToQuery(msg)
}

func (m model) onBrowseBack() (tea.Model, tea.Cmd) {
	m.drill.tok++
	m.loading = false
	if m.query.Value() != "" {
		m.query.SetValue("")
		m.queryTok++
		m.applyMode()
		m.resize() // the discover filter bar reappears on query clear
		return m, m.inspector.hover()
	}
	if m.mode == modeDiscover {
		return m, m.exitDiscover()
	}
	return m, nil
}

func (m model) updateDrilldown(msg tea.Msg) (tea.Model, tea.Cmd) {
	before := m.drill.tok
	out := m.drill.update(msg, m.keys)
	if before != m.drill.tok && !out.loading {
		m.loading = false
	}
	switch {
	case out.selected != nil:
		return m.choose(out.selected)
	case out.exit:
		m.loading = false
		m.goTo(screenBrowse)
		return m, nil
	case out.loading:
		m.loading = true
		m.err = nil
		return m, tea.Batch(out.cmd, m.spin.Tick)
	}
	return m, out.cmd
}

// spinning is whether anything on screen shows the spinner.
func (m model) spinning() bool {
	return m.loading || (m.genres.draft != nil && !m.genres.loaded && m.genres.err == nil)
}

func (m model) drilldownFiltering() bool {
	return m.scr == screenDrilldown && m.drill.list.SettingFilter()
}

// goTo is the only way to change screen; different chrome needs recompute.
func (m *model) goTo(s screen) {
	m.scr = s
	m.resize()
}

func (m *model) resize() {
	h := m.bodyHeight()
	m.results.SetSize(max(m.w-posterCols-spGutter, 30), h)
	m.drill.setSize(m.w, h)
	m.query.SetWidth(max(m.w-spInline*2, 20))
}
