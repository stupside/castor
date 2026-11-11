package browse

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stupside/castor/cmd/castor/internal/browse/tmdb"
)

// searchDebounce waits out a burst of keystrokes before searching.
const searchDebounce = 250 * time.Millisecond

type searchTickMsg struct {
	tok   int
	query string
}

type searchDoneMsg struct {
	tok int
	res []tmdb.SearchResult
	err error
}

func searchTickCmd(tok int, query string) tea.Cmd {
	return tea.Tick(searchDebounce, func(time.Time) tea.Msg {
		return searchTickMsg{tok: tok, query: query}
	})
}

func searchCmd(ctx context.Context, c Catalog, tok int, q string) tea.Cmd {
	return func() tea.Msg {
		res, err := c.Search(ctx, q)
		return searchDoneMsg{tok: tok, res: res, err: err}
	}
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

// forwardToQuery debounces search and reflows if discover filter appears/disappears.
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
	// A query changed by any means, a paste included, leaves a show still loading behind.
	m.drill.abandon()
	return m, tea.Batch(cmd, searchTickCmd(m.queryTok, m.query.Value()))
}
