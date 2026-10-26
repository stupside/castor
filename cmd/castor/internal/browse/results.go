package browse

import (
	"fmt"
	"strconv"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"github.com/stupside/castor/cmd/castor/internal/browse/tmdb"
)

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
		return m.choose(&Selection{Kind: KindMovie, TMDBID: strconv.Itoa(r.ID), Title: r.DisplayTitle(), Synopsis: r.Overview, ImageURL: tmdb.ImageURL(r.PosterPath), ReleaseDate: r.ReleaseDate})
	case tmdb.MediaTV:
		m.loading = true
		m.err = nil
		return m, tea.Batch(m.drill.begin(r.ID, r.DisplayTitle(), r.PosterPath), m.spin.Tick)
	}
	return m, nil
}

func (m model) selectedResult() *tmdb.SearchResult {
	if it, ok := m.results.SelectedItem().(resultItem); ok {
		return &it.r
	}
	return nil
}
