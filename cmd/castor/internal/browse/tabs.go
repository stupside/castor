package browse

import (
	"context"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"github.com/stupside/castor/cmd/castor/internal/browse/tmdb"
)

type tabID int

const (
	tabTrending tabID = iota
	tabPopularMovies
	tabTopMovies
	tabPopularTV
	tabTopTV
	tabCount
	noTab tabID = -1
)

func (t tabID) label() string {
	return [...]string{"Trending", "Popular Movies", "Top Movies", "Popular TV", "Top TV"}[t]
}

// fetch uses discover, not TMDB's popular/top_rated lists, so unreleased titles stay out.
func (t tabID) fetch(ctx context.Context, c Catalog) ([]tmdb.SearchResult, error) {
	var p tmdb.DiscoverParams
	switch t {
	case tabPopularMovies:
		p = tmdb.DiscoverParams{MediaType: tmdb.MediaMovie, Sort: tmdb.SortPopularity}
	case tabTopMovies:
		p = tmdb.DiscoverParams{MediaType: tmdb.MediaMovie, Sort: tmdb.SortRating}
	case tabPopularTV:
		p = tmdb.DiscoverParams{MediaType: tmdb.MediaTV, Sort: tmdb.SortPopularity}
	case tabTopTV:
		p = tmdb.DiscoverParams{MediaType: tmdb.MediaTV, Sort: tmdb.SortRating}
	default:
		return c.Trending(ctx)
	}
	pg, err := c.Discover(ctx, p)
	return pg.Results, err
}

// tabOwnsList is whether the list belongs to the chosen tab: no discover and no query.
func (m *model) tabOwnsList() bool {
	return m.mode == modeCurated && m.query.Value() == ""
}

// setRows puts items in the list as the rows of tab, noTab for search or discover results.
func (m *model) setRows(items []list.Item, tab tabID) {
	m.results.SetItems(items)
	m.listTab = tab
}

func (m *model) cycleTab(delta int) tea.Cmd {
	if m.listTab != noTab {
		m.topsCursor[m.listTab] = m.results.Index()
	}
	n := int(tabCount)
	m.tab = tabID(((int(m.tab)+delta)%n + n) % n)
	if m.mode != modeCurated {
		m.mode = modeCurated
		m.disc.tok++
		m.disc.loadingMore = false
		m.resize()
	}
	return m.ensureTabLoaded()
}

func (m *model) applyTab() {
	m.setRows(m.topsCache[m.tab], m.tab)
	if m.topsCursor[m.tab] < len(m.topsCache[m.tab]) {
		m.results.Select(m.topsCursor[m.tab])
	}
}

// applyMode restores feed after query clears; spinner/error were abandoned.
func (m *model) applyMode() {
	m.loading = false
	m.err = nil
	if m.mode == modeDiscover {
		m.setRows(toResultItems(m.disc.results), noTab)
		m.results.Select(0)
		return
	}
	m.applyTab()
}

func (m *model) ensureTabLoaded() tea.Cmd {
	if m.topsCache[m.tab] != nil {
		m.loading = false
		m.err = nil
		m.applyTab()
		return m.inspector.hover()
	}
	m.loading = true
	m.err = nil
	return tea.Batch(loadTopCmd(m.ctx, m.client, m.tab), m.spin.Tick)
}

type topsLoadedMsg struct {
	tab tabID
	res []tmdb.SearchResult
	err error
}

func loadTopCmd(ctx context.Context, c Catalog, t tabID) tea.Cmd {
	return func() tea.Msg {
		res, err := t.fetch(ctx, c)
		return topsLoadedMsg{tab: t, res: res, err: err}
	}
}

func (m model) onTopsLoaded(msg topsLoadedMsg) (tea.Model, tea.Cmd) {
	current := msg.tab == m.tab && m.tabOwnsList()
	if current {
		m.loading = false
	}
	if msg.err != nil {
		if current {
			m.err = msg.err
		}
		return m, nil
	}
	m.topsCache[msg.tab] = toResultItems(msg.res)
	if current {
		m.applyTab()
		return m, m.inspector.hover()
	}
	return m, nil
}
