// Package browse is a Bubble Tea TUI for searching TMDB and picking a movie or
// TV episode to cast. It does not touch the cast pipeline — Run returns the
// user's Selection and the caller hands it off.
//
// The screen is composed from focused parts, each owning its own state:
//
//	model       — this file: the results browser (curated tabs / search /
//	              discover feed) plus screen routing and layout.
//	inspector   — the poster + metadata panel and its async asset loading.
//	genrePicker — the modal genre filter.
//	drilldown   — the TV seasons → episodes navigation.
//	tmdb.Client — the read-only data source.
package browse

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/stupside/castor/internal/browse/tmdb"
)

// ---------------------------------------------------------------- public API

type Kind int

const (
	KindNone Kind = iota
	KindMovie
	KindEpisode
)

type Selection struct {
	Kind    Kind
	TMDBID  string
	Title   string
	Season  uint
	Episode uint
}

// Run blocks on the TUI until the user picks or quits.
func Run(ctx context.Context, client *tmdb.Client) (Selection, error) {
	final, err := tea.NewProgram(newModel(ctx, client), tea.WithAltScreen()).Run()
	if err != nil {
		return Selection{}, err
	}
	if fm, ok := final.(model); ok {
		return fm.sel, nil
	}
	return Selection{}, nil
}

// ---------------------------------------------------------------- constants

const (
	// Poster footprint in terminal cells. Half-block rendering means each cell
	// shows 2 stacked pixels vertically, so the rendered pixel grid is
	// posterCols × (posterRows*2). 27 × 40 ≈ 2:3 — the canonical movie-poster
	// aspect ratio. Sizing the box correctly stops pixterm from stretching the
	// image horizontally.
	posterCols     = 27
	posterRows     = 20
	searchDebounce = 250 * time.Millisecond
)

type screen int

const (
	screenBrowse screen = iota
	screenDrilldown
)

// ---------------------------------------------------------------- tabs

type tabID int

const (
	tabTrending tabID = iota
	tabPopularMovies
	tabTopMovies
	tabPopularTV
	tabTopTV
	tabCount
)

func (t tabID) label() string {
	return [...]string{"Trending", "Popular Movies", "Top Movies", "Popular TV", "Top TV"}[t]
}

func (t tabID) fetch(ctx context.Context, c *tmdb.Client) ([]tmdb.SearchResult, error) {
	switch t {
	case tabPopularMovies:
		return c.PopularMovies(ctx)
	case tabTopMovies:
		return c.TopRatedMovies(ctx)
	case tabPopularTV:
		return c.PopularTV(ctx)
	case tabTopTV:
		return c.TopRatedTV(ctx)
	default:
		return c.Trending(ctx)
	}
}

// ---------------------------------------------------------------- result item

type resultItem struct{ r tmdb.SearchResult }

func (i resultItem) Title() string {
	if y := i.r.Year(); y != "" {
		return fmt.Sprintf("%s (%s)", i.r.DisplayTitle(), y)
	}
	return i.r.DisplayTitle()
}

func (i resultItem) Description() string {
	typ := "Movie"
	if i.r.MediaType == tmdb.MediaTV {
		typ = "TV"
	}
	if i.r.Overview == "" {
		return typ
	}
	return typ + " · " + truncate(i.r.Overview, 80)
}

func (i resultItem) FilterValue() string { return i.r.DisplayTitle() }

func toResultItems(rs []tmdb.SearchResult) []list.Item {
	items := make([]list.Item, len(rs))
	for i, r := range rs {
		items[i] = resultItem{r: r}
	}
	return items
}

// ---------------------------------------------------------------- model

type model struct {
	ctx    context.Context
	client *tmdb.Client

	styles styles
	keys   keyMap
	help   help.Model
	spin   spinner.Model

	scr screen

	// results browser (screenBrowse)
	tab        tabID
	mode       browseMode
	query      textinput.Model
	queryTok   int // monotonic; only the latest debounce tick fires
	results    list.Model
	topsCache  [tabCount][]list.Item
	topsLoaded [tabCount]bool
	topsCursor [tabCount]int
	disc       discoverState

	// composed parts
	inspector inspector
	picker    genrePicker
	drill     drilldown

	loading bool
	err     error

	sel  Selection
	w, h int
}

func newModel(ctx context.Context, client *tmdb.Client) model {
	st := newStyles()
	hlp := newHelp()

	q := textinput.New()
	q.Placeholder = "Type to search TMDB…"
	q.Prompt = "❯ "
	q.PromptStyle = lipgloss.NewStyle().Foreground(accent)
	q.PlaceholderStyle = lipgloss.NewStyle().Foreground(fgMuted)
	q.TextStyle = lipgloss.NewStyle().Foreground(fgPrimary)
	q.CharLimit = 128
	q.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = lipgloss.NewStyle().Foreground(accent)

	delegate := newDelegate()

	results := list.New(nil, delegate, 0, 0)
	results.SetShowTitle(false)
	results.SetShowStatusBar(false)
	results.SetShowHelp(false)
	results.SetFilteringEnabled(false) // the textinput owns filtering on browse

	return model{
		ctx:       ctx,
		client:    client,
		styles:    st,
		keys:      defaultKeys(),
		help:      hlp,
		spin:      sp,
		scr:       screenBrowse,
		tab:       tabTrending,
		mode:      modeCurated,
		query:     q,
		results:   results,
		disc:      discoverState{sort: tmdb.SortPopularity},
		inspector: newInspector(ctx, client, st),
		picker:    newGenrePicker(st, hlp),
		drill:     newDrilldown(ctx, client, delegate),
		loading:   true,
	}
}

func newDelegate() list.DefaultDelegate {
	d := list.NewDefaultDelegate()
	d.Styles.NormalTitle = d.Styles.NormalTitle.Foreground(fgPrimary)
	d.Styles.NormalDesc = d.Styles.NormalDesc.Foreground(fgMuted)
	d.Styles.SelectedTitle = d.Styles.SelectedTitle.Foreground(accent).BorderForeground(accent).Bold(true)
	d.Styles.SelectedDesc = d.Styles.SelectedDesc.Foreground(fgSecondary).BorderForeground(accent)
	d.Styles.DimmedTitle = d.Styles.DimmedTitle.Foreground(fgMuted)
	d.Styles.DimmedDesc = d.Styles.DimmedDesc.Foreground(fgMuted)
	d.Styles.FilterMatch = lipgloss.NewStyle().Foreground(accent).Underline(true)
	return d
}

func newHelp() help.Model {
	h := help.New()
	h.ShowAll = false
	h.Styles.ShortKey = h.Styles.ShortKey.Foreground(fgSecondary)
	h.Styles.ShortDesc = h.Styles.ShortDesc.Foreground(fgMuted)
	h.Styles.ShortSeparator = h.Styles.ShortSeparator.Foreground(fgMuted)
	h.Styles.FullKey = h.Styles.FullKey.Foreground(fgSecondary)
	h.Styles.FullDesc = h.Styles.FullDesc.Foreground(fgMuted)
	h.Styles.FullSeparator = h.Styles.FullSeparator.Foreground(fgMuted)
	h.Styles.Ellipsis = h.Styles.Ellipsis.Foreground(fgMuted)
	return h
}

// ---------------------------------------------------------------- results messages

type topsLoadedMsg struct {
	tab tabID
	res []tmdb.SearchResult
	err error
}

type searchTickMsg struct {
	tok   int
	query string
}

type searchDoneMsg struct {
	tok int
	res []tmdb.SearchResult
	err error
}

func loadTopCmd(ctx context.Context, c *tmdb.Client, t tabID) tea.Cmd {
	return func() tea.Msg {
		res, err := t.fetch(ctx, c)
		return topsLoadedMsg{tab: t, res: res, err: err}
	}
}

func searchTickCmd(tok int, query string) tea.Cmd {
	return tea.Tick(searchDebounce, func(time.Time) tea.Msg {
		return searchTickMsg{tok: tok, query: query}
	})
}

func searchCmd(ctx context.Context, c *tmdb.Client, tok int, q string) tea.Cmd {
	return func() tea.Msg {
		res, err := c.SearchMulti(ctx, q)
		return searchDoneMsg{tok: tok, res: res, err: err}
	}
}

// ---------------------------------------------------------------- tea.Model

func (m model) Init() tea.Cmd {
	return tea.Batch(
		m.spin.Tick,
		textinput.Blink,
		loadTopCmd(m.ctx, m.client, m.tab),
		loadGenresCmd(m.ctx, m.client),
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.help.Width = msg.Width
		m.resize()
		if m.picker.shown {
			m.picker.resize(m.w, m.h)
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case tea.KeyMsg:
		if key.Matches(msg, m.keys.Quit) {
			return m, tea.Quit
		}
		if m.picker.shown {
			cmd, action := m.picker.update(msg, m.keys, m.w, m.h)
			if action == genreApplied {
				cmd = m.enterDiscover()
			}
			return m, cmd
		}
		if key.Matches(msg, m.keys.Help) && !m.drilldownFiltering() {
			m.help.ShowAll = !m.help.ShowAll
			m.resize()
			return m, nil
		}

	case genresLoadedMsg:
		if msg.err == nil {
			m.picker.setCatalog(msg.cat)
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
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.drill.showSeasons(msg.tv)
		m.scr = screenDrilldown
		return m, nil

	case seasonDoneMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.drill.showEpisodes(msg.sd)
		return m, nil
	}

	switch m.scr {
	case screenBrowse:
		return m.updateBrowse(msg)
	case screenDrilldown:
		return m.updateDrilldown(msg)
	}
	return m, nil
}

func (m model) onTopsLoaded(msg topsLoadedMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	if msg.err != nil {
		m.err = msg.err
		return m, nil
	}
	items := toResultItems(msg.res)
	m.topsCache[msg.tab] = items
	m.topsLoaded[msg.tab] = true
	if m.scr == screenBrowse && m.mode == modeCurated && m.tab == msg.tab && m.query.Value() == "" {
		m.results.SetItems(items)
		m.results.Select(m.topsCursor[msg.tab])
		return m, m.inspector.hover()
	}
	return m, nil
}

func (m model) onSearchTick(msg searchTickMsg) (tea.Model, tea.Cmd) {
	if msg.tok != m.queryTok {
		return m, nil // stale; a newer keystroke supersedes this tick
	}
	if msg.query == "" {
		m.applyMode()
		return m, m.inspector.hover()
	}
	m.loading = true
	m.err = nil
	return m, tea.Batch(searchCmd(m.ctx, m.client, msg.tok, msg.query), m.spin.Tick)
}

// ---------------------------------------------------------------- browse update

func (m model) updateBrowse(msg tea.Msg) (tea.Model, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m.forwardToQuery(msg)
	}
	switch {
	case key.Matches(km, m.keys.Genres):
		m.picker.open(m.w, m.h)
		return m, nil
	case key.Matches(km, m.keys.Sort):
		if m.mode == modeDiscover {
			return m, m.cycleSort()
		}
		return m, nil
	case key.Matches(km, m.keys.Media):
		if m.mode == modeDiscover {
			m.picker.toggleMedia(m.w, m.h)
			return m, m.enterDiscover()
		}
		return m, nil
	case key.Matches(km, m.keys.Tab):
		if m.query.Value() == "" {
			return m, m.cycleTab(1)
		}
		return m, nil
	case key.Matches(km, m.keys.ShiftTab):
		if m.query.Value() == "" {
			return m, m.cycleTab(-1)
		}
		return m, nil
	case key.Matches(km, m.keys.Enter):
		if r := m.selectedResult(); r != nil {
			return m.pickResult(*r)
		}
		return m, nil
	case key.Matches(km, m.keys.Back):
		return m.onBrowseBack()
	case key.Matches(km, m.keys.Up),
		key.Matches(km, m.keys.Down),
		key.Matches(km, m.keys.PageUp),
		key.Matches(km, m.keys.PageDown):
		return m.delegateResults(msg)
	}
	return m.forwardToQuery(msg)
}

// forwardToQuery sends a message to the search input and, when the query text
// changed, kicks a debounced search (reflowing if the discover filter bar
// appeared or disappeared).
func (m model) forwardToQuery(msg tea.Msg) (tea.Model, tea.Cmd) {
	prev := m.query.Value()
	var cmd tea.Cmd
	m.query, cmd = m.query.Update(msg)
	if m.query.Value() == prev {
		return m, cmd
	}
	if (prev == "") != (m.query.Value() == "") {
		m.resize()
	}
	m.queryTok++
	return m, tea.Batch(cmd, searchTickCmd(m.queryTok, m.query.Value()))
}

func (m model) onBrowseBack() (tea.Model, tea.Cmd) {
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

func (m *model) cycleTab(delta int) tea.Cmd {
	m.topsCursor[m.tab] = m.results.Index()
	n := int(tabCount)
	m.tab = tabID(((int(m.tab)+delta)%n + n) % n)
	if m.mode != modeCurated {
		m.mode = modeCurated
		m.resize()
	}
	return m.ensureTabLoaded()
}

func (m *model) applyTab() {
	m.results.SetItems(m.topsCache[m.tab])
	if m.topsCursor[m.tab] < len(m.topsCache[m.tab]) {
		m.results.Select(m.topsCursor[m.tab])
	}
}

// applyMode restores the underlying feed (curated tab or discover results)
// after a search query is cleared.
func (m *model) applyMode() {
	if m.mode == modeDiscover {
		m.results.SetItems(toResultItems(m.disc.results))
		m.results.Select(0)
		return
	}
	m.applyTab()
}

func (m *model) ensureTabLoaded() tea.Cmd {
	if m.topsLoaded[m.tab] {
		m.applyTab()
		return m.inspector.hover()
	}
	m.loading = true
	m.err = nil
	return tea.Batch(loadTopCmd(m.ctx, m.client, m.tab), m.spin.Tick)
}

func (m model) delegateResults(msg tea.Msg) (tea.Model, tea.Cmd) {
	prev := m.results.Index()
	var cmd tea.Cmd
	m.results, cmd = m.results.Update(msg)
	cmds := []tea.Cmd{cmd}
	if m.results.Index() != prev {
		cmds = append(cmds, m.inspector.hover(), m.maybeLoadMore())
	}
	return m, tea.Batch(cmds...)
}

func (m model) pickResult(r tmdb.SearchResult) (tea.Model, tea.Cmd) {
	switch r.MediaType {
	case tmdb.MediaMovie:
		m.sel = Selection{Kind: KindMovie, TMDBID: strconv.Itoa(r.ID), Title: r.DisplayTitle()}
		return m, tea.Quit
	case tmdb.MediaTV:
		m.loading = true
		m.err = nil
		return m, tea.Batch(m.drill.begin(r.ID, r.DisplayTitle()), m.spin.Tick)
	}
	return m, nil
}

func (m model) selectedResult() *tmdb.SearchResult {
	if it, ok := m.results.SelectedItem().(resultItem); ok {
		return &it.r
	}
	return nil
}

// ---------------------------------------------------------------- drilldown update

func (m model) updateDrilldown(msg tea.Msg) (tea.Model, tea.Cmd) {
	out := m.drill.update(msg, m.keys)
	switch {
	case out.selected != nil:
		m.sel = *out.selected
		return m, tea.Quit
	case out.exit:
		m.scr = screenBrowse
		return m, nil
	case out.loading:
		m.loading = true
		m.err = nil
		return m, tea.Batch(out.cmd, m.spin.Tick)
	}
	return m, out.cmd
}

func (m model) drilldownFiltering() bool {
	return m.scr == screenDrilldown && m.drill.filtering()
}

// ---------------------------------------------------------------- layout

func (m *model) resize() {
	h := m.bodyHeight()
	m.results.SetSize(max(m.w-posterCols-spGutter, 30), h)
	m.drill.setSize(m.w, h)
	m.query.Width = max(m.w-spInline*2, 20)
}

// bodyHeight is the fixed list/poster row height: total minus footer and the
// chrome above the body (header, query, discover filter bar, blank spacers).
func (m model) bodyHeight() int {
	chrome := 3 // drilldown: header + 2 blanks
	if m.scr == screenBrowse {
		chrome = 4 // header + query + 2 blanks
		if m.mode == modeDiscover && m.query.Value() == "" {
			chrome++ // filter bar
		}
	}
	return max(m.h-lipgloss.Height(m.footer())-chrome, 8)
}

// ---------------------------------------------------------------- view

func (m model) View() string {
	switch {
	case m.picker.shown:
		return m.picker.view(m.spin, m.w, m.h)
	case m.scr == screenDrilldown:
		return lipgloss.JoinVertical(lipgloss.Left, m.drill.view(m.styles), "", m.footer())
	default:
		return m.viewBrowse()
	}
}

func (m model) viewBrowse() string {
	rows := []string{m.browseHeader(), m.query.View()}
	if m.mode == modeDiscover && m.query.Value() == "" {
		rows = append(rows, m.filterBar())
	}
	rows = append(rows, "", m.bodyRow(), "", m.footer())
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

// browseHeader puts the active label flush-left and a mode indicator flush-right
// via lipgloss.PlaceHorizontal — tab dots on curated, media type on discover.
// When searching, the query echo is dropped from the title because the
// textinput right below already shows it.
func (m model) browseHeader() string {
	var label, rhs string
	switch {
	case m.query.Value() != "":
		label = "Search"
	case m.mode == modeDiscover:
		label = "Discover"
		rhs = m.styles.Muted.Render(m.picker.mediaLabel())
	default:
		label = m.tab.label()
		rhs = m.renderTabDots()
	}
	title := m.styles.Title.Render(label)
	placed := lipgloss.PlaceHorizontal(max(m.w-lipgloss.Width(title), 0), lipgloss.Right, rhs)
	return lipgloss.JoinHorizontal(lipgloss.Top, title, placed)
}

// filterBar summarizes the active discover filters (genres + sort) on the left
// and echoes the discover key hints on the right.
func (m model) filterBar() string {
	summary := "All genres"
	if names := m.picker.selectedNames(); len(names) > 0 {
		summary = strings.Join(names, ", ")
	}
	left := lipgloss.NewStyle().Padding(0, spInline).Render(
		m.styles.MetaTitle.Render(truncate(summary, max(m.w/2, 12))) +
			m.styles.Muted.Render("  ·  Sort: "+m.disc.sort.Label()),
	)
	hints := m.help.Styles.ShortDesc.Render("^g genres · ^s sort · ^t movie/tv")
	rhs := lipgloss.PlaceHorizontal(max(m.w-lipgloss.Width(left), 0), lipgloss.Right, hints)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, rhs)
}

func (m model) renderTabDots() string {
	active := lipgloss.NewStyle().Foreground(accent)
	inactive := lipgloss.NewStyle().Foreground(fgMuted)
	parts := make([]string, tabCount)
	for i := range tabCount {
		if i == m.tab {
			parts[i] = active.Render("●")
		} else {
			parts[i] = inactive.Render("○")
		}
	}
	return strings.Join(parts, " ")
}

// bodyRow renders the results list + inspector panel as one fixed-height row.
func (m model) bodyRow() string {
	h := m.bodyHeight()
	left := m.results.View()
	right := m.inspector.view(m.selectedResult(), h)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", spGutter), right)
}

// ---------------------------------------------------------------- footer

// footer always renders the help line + a status row so the body height does
// not jitter as loading/error transitions toggle.
func (m model) footer() string {
	pad := lipgloss.NewStyle().Padding(0, spInline)
	helpLine := pad.Render(m.help.View(screenKeys{k: m.keys, s: m.scr, discover: m.mode == modeDiscover}))
	status := m.statusLine()
	if status == "" {
		status = " " // reserve the row
	}
	return lipgloss.JoinVertical(lipgloss.Left, helpLine, status)
}

func (m model) statusLine() string {
	pad := lipgloss.NewStyle().Padding(0, spInline)
	switch {
	case m.loading:
		return pad.Render(m.spin.View() + " " + m.styles.Muted.Render("loading…"))
	case m.err != nil:
		return m.styles.Err.Render("error: " + m.err.Error())
	}
	return ""
}
