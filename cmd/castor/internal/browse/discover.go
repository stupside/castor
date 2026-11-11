package browse

import (
	"context"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/stupside/castor/cmd/castor/internal/browse/tmdb"
)

// browseMode is the source of the results list on screenBrowse: non-empty search overrides both modes.
type browseMode int

const (
	modeCurated browseMode = iota
	modeDiscover
)

// sorts is the order ^s cycles through.
var sorts = []tmdb.Sort{tmdb.SortPopularity, tmdb.SortRating, tmdb.SortNewest}

func sortLabel(s tmdb.Sort) string {
	switch s {
	case tmdb.SortRating:
		return "Rating"
	case tmdb.SortNewest:
		return "Newest"
	default:
		return "Popularity"
	}
}

// discoverState holds the sort, accumulated pages, token that invalidates in-flight pages on filter change.
type discoverState struct {
	sort        tmdb.Sort
	results     []tmdb.SearchResult
	page        int
	hasMore     bool
	loadingMore bool
	tok         int
}

type discoverDoneMsg struct {
	tok        int
	page       int
	res        []tmdb.SearchResult
	totalPages int
	err        error
}

func discoverCmd(ctx context.Context, c Catalog, tok int, p tmdb.DiscoverParams) tea.Cmd {
	return func() tea.Msg {
		pg, err := c.Discover(ctx, p)
		return discoverDoneMsg{tok: tok, page: p.Page, res: pg.Results, totalPages: pg.TotalPages, err: err}
	}
}

// discParams snapshots the genre filter and the current sort as a query.
func (m model) discParams(page int) tmdb.DiscoverParams {
	genres := m.genres.applied
	return tmdb.DiscoverParams{
		MediaType: genres.media,
		GenreIDs:  genres.genreIDs(),
		Sort:      m.disc.sort,
		Page:      page,
	}
}

func (m *model) enterDiscover() tea.Cmd {
	m.drill.tok++
	m.mode = modeDiscover
	m.query.SetValue("")
	m.queryTok++
	m.disc = discoverState{sort: m.disc.sort, page: 1, tok: m.disc.tok + 1}
	m.loading = true
	m.err = nil
	m.resize()
	return tea.Batch(discoverCmd(m.ctx, m.client, m.disc.tok, m.discParams(1)), m.spin.Tick)
}

func (m *model) exitDiscover() tea.Cmd {
	m.mode = modeCurated
	m.disc.tok++
	m.disc.loadingMore = false
	m.resize()
	return m.ensureTabLoaded()
}

// cycleSort advances the sort and refetches from page one.
func (m *model) cycleSort() tea.Cmd {
	i := slices.Index(sorts, m.disc.sort)
	m.disc.sort = sorts[(i+1)%len(sorts)]
	return m.enterDiscover()
}

func (m *model) onDiscoverDone(msg discoverDoneMsg) tea.Cmd {
	if m.mode != modeDiscover || msg.tok != m.disc.tok {
		return nil // stale: filters changed while this was in flight
	}
	searching := m.query.Value() != "" // the rows and status then belong to the search
	if !searching {
		m.loading = false
	}
	m.disc.loadingMore = false
	if msg.err != nil {
		if !searching {
			m.err = msg.err
		}
		return nil
	}
	m.disc.page = msg.page
	m.disc.hasMore = msg.page < msg.totalPages
	if msg.page <= 1 {
		m.disc.results = msg.res
	} else {
		m.disc.results = append(m.disc.results, msg.res...)
	}
	if searching {
		return nil // applyMode shows them when the query clears
	}
	m.setRows(toResultItems(m.disc.results), noTab)
	if msg.page <= 1 {
		m.results.Select(0)
	}
	return m.inspector.hover()
}

func (m *model) maybeLoadMore() tea.Cmd {
	if m.mode != modeDiscover || m.query.Value() != "" {
		return nil
	}
	if m.disc.loadingMore || !m.disc.hasMore {
		return nil
	}
	if m.results.Index() < len(m.disc.results)-3 {
		return nil
	}
	m.disc.loadingMore = true
	return discoverCmd(m.ctx, m.client, m.disc.tok, m.discParams(m.disc.page+1))
}
