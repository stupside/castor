package browse

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stupside/castor/internal/browse/tmdb"
)

func TestFormatRuntime(t *testing.T) {
	cases := map[int]string{0: "", -5: "", 45: "45m", 60: "1h", 128: "2h 8m"}
	for in, want := range cases {
		if got := formatRuntime(in); got != want {
			t.Errorf("formatRuntime(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		s     string
		width int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello", 4, "hel…"},
		{"hello", 1, "…"},
		{"hello", 0, ""},
		{"héllo", 4, "hél…"}, // rune-aware: é is not split mid-byte
	}
	for _, c := range cases {
		if got := truncate(c.s, c.width); got != c.want {
			t.Errorf("truncate(%q,%d) = %q, want %q", c.s, c.width, got, c.want)
		}
	}
}

func TestMetaLines(t *testing.T) {
	r := tmdb.SearchResult{VoteAverage: 8.1}

	// Without details, only the rating is known.
	info, tagline, cast := metaLines(r, nil, 60)
	if info != "★ 8.1" || tagline != "" || cast != "" {
		t.Fatalf("nil details: info=%q tagline=%q cast=%q", info, tagline, cast)
	}

	// With details, runtime + genres enrich the info line and cast appears.
	d := &tmdb.Details{
		Runtime: 128,
		Tagline: "It begins.",
		Genres:  []tmdb.Genre{{ID: 28, Name: "Action"}, {ID: 878, Name: "Science Fiction"}},
	}
	d.Credits.Cast = []struct {
		Name string `json:"name"`
	}{{Name: "A"}, {Name: "B"}, {Name: "C"}, {Name: "D"}}

	info, tagline, cast = metaLines(r, d, 80)
	if info != "★ 8.1 · 2h 8m · Action, Science Fiction" {
		t.Errorf("info = %q", info)
	}
	if tagline != "It begins." {
		t.Errorf("tagline = %q", tagline)
	}
	if cast != "With A, B, C" {
		t.Errorf("cast = %q", cast)
	}

	// Narrow width truncates the info line.
	if info, _, _ = metaLines(r, d, 10); len([]rune(info)) != 10 {
		t.Errorf("narrow info not clamped to 10 runes: %q", info)
	}
}

// --- headless model driver -------------------------------------------------

func drive(t *testing.T, m model, msg tea.Msg) (model, tea.Cmd) {
	t.Helper()
	tm, cmd := m.Update(msg)
	mm, ok := tm.(model)
	if !ok {
		t.Fatalf("Update returned %T, not model", tm)
	}
	return mm, cmd
}

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func fakeResults(n int) []tmdb.SearchResult {
	rs := make([]tmdb.SearchResult, n)
	for i := range rs {
		rs[i] = tmdb.SearchResult{ID: i + 1, MediaType: tmdb.MediaMovie, Title: fmt.Sprintf("Movie %d", i+1), VoteAverage: 7}
	}
	return rs
}

// TestBrowseDiscoverFlow drives the full genre → discover → paginate → search
// path through Update/View headlessly, asserting state transitions and that no
// render panics along the way.
func TestBrowseDiscoverFlow(t *testing.T) {
	m := newModel(context.Background(), tmdb.New("dummy", 0), "", "")

	m, _ = drive(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	mustRender(t, m)

	// Genre catalog + a curated tab arrive.
	cat := tmdb.GenreCatalog{
		Movie: []tmdb.Genre{{ID: 28, Name: "Action"}, {ID: 35, Name: "Comedy"}, {ID: 878, Name: "Science Fiction"}},
		TV:    []tmdb.Genre{{ID: 10759, Name: "Action & Adventure"}, {ID: 35, Name: "Comedy"}},
	}
	m, _ = drive(t, m, genresLoadedMsg{cat: cat})
	m, _ = drive(t, m, topsLoadedMsg{tab: tabTrending, res: fakeResults(3)})
	if got := len(m.results.Items()); got != 3 {
		t.Fatalf("curated items = %d, want 3", got)
	}
	mustRender(t, m)

	// Open the genre picker.
	m, _ = drive(t, m, tea.KeyMsg{Type: tea.KeyCtrlG})
	if !m.picker.shown {
		t.Fatal("ctrl+g did not open the genre overlay")
	}
	if got := len(m.picker.list.Items()); got != len(cat.Movie) {
		t.Fatalf("overlay shows %d genres, want %d", got, len(cat.Movie))
	}
	mustRender(t, m)

	// Toggle the first genre (Action).
	m, _ = drive(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if !m.picker.selected[28] {
		t.Fatal("space did not select Action")
	}

	// Switch to TV inside the overlay: selection clears, catalog swaps.
	m, _ = drive(t, m, runes("m"))
	if m.picker.media != tmdb.MediaTV {
		t.Fatalf("overlay media = %q, want tv", m.picker.media)
	}
	if len(m.picker.selected) != 0 {
		t.Fatal("switching media should clear genre selection")
	}
	if got := len(m.picker.list.Items()); got != len(cat.TV) {
		t.Fatalf("overlay now shows %d genres, want %d (tv)", got, len(cat.TV))
	}
	// Back to movies and reselect Action for the apply.
	m, _ = drive(t, m, runes("m"))
	m, _ = drive(t, m, tea.KeyMsg{Type: tea.KeySpace})

	// Apply → Discover mode, a fetch is issued.
	m, cmd := drive(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.picker.shown {
		t.Fatal("apply should close the overlay")
	}
	if m.mode != modeDiscover {
		t.Fatal("apply should enter discover mode")
	}
	if cmd == nil {
		t.Fatal("apply should issue a discover command")
	}
	mustRender(t, m)

	// First discover page (5 results, 5 pages total ⇒ more remain).
	m, _ = drive(t, m, discoverDoneMsg{tok: m.disc.tok, page: 1, res: fakeResults(5), totalPages: 5})
	if got := len(m.results.Items()); got != 5 {
		t.Fatalf("discover items = %d, want 5", got)
	}
	if !m.disc.hasMore {
		t.Fatal("discHasMore should be true (page 1 of 5)")
	}
	mustRender(t, m)

	// Scroll toward the end to trigger a page-2 prefetch.
	m, _ = drive(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = drive(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if !m.disc.loadingMore {
		t.Fatal("nearing the end should start loading page 2")
	}
	m, _ = drive(t, m, discoverDoneMsg{tok: m.disc.tok, page: 2, res: fakeResults(5), totalPages: 5})
	if got := len(m.disc.results); got != 10 {
		t.Fatalf("after page 2, discResults = %d, want 10", got)
	}
	if m.disc.loadingMore {
		t.Fatal("discLoadingMore should reset after page 2 lands")
	}

	// A stale page (wrong token) must be ignored.
	before := len(m.disc.results)
	m, _ = drive(t, m, discoverDoneMsg{tok: m.disc.tok - 99, page: 3, res: fakeResults(5), totalPages: 5})
	if len(m.disc.results) != before {
		t.Fatal("stale discover page should have been dropped")
	}

	// Cycle sort → refetch issued, token advances.
	prevTok := m.disc.tok
	m, cmd = drive(t, m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if m.disc.sort != tmdb.SortRating || cmd == nil || m.disc.tok == prevTok {
		t.Fatalf("ctrl+s should cycle sort and refetch (sort=%v tok=%d→%d)", m.disc.sort, prevTok, m.disc.tok)
	}
	m, _ = drive(t, m, discoverDoneMsg{tok: m.disc.tok, page: 1, res: fakeResults(4), totalPages: 1})
	if m.disc.hasMore {
		t.Fatal("single-page result should clear discHasMore")
	}

	// Type a search query (overrides the feed), then clear it (restores feed).
	m, _ = drive(t, m, runes("a"))
	if m.query.Value() != "a" {
		t.Fatalf("query = %q, want a", m.query.Value())
	}
	m, _ = drive(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.query.Value() != "" || m.mode != modeDiscover {
		t.Fatalf("esc should clear query and stay in discover (query=%q mode=%v)", m.query.Value(), m.mode)
	}

	// Esc again exits discover back to the curated tabs.
	m, _ = drive(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != modeCurated {
		t.Fatal("esc in discover (empty query) should return to curated")
	}
	mustRender(t, m)
}

// TestGenreOverlayDoesNotQuitOnQ guards the fix for the genre list's default
// "q" keybinding quitting the whole program when forwarded from the overlay.
func TestGenreOverlayDoesNotQuitOnQ(t *testing.T) {
	m := newModel(context.Background(), tmdb.New("dummy", 0), "", "")
	m, _ = drive(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = drive(t, m, genresLoadedMsg{cat: tmdb.GenreCatalog{
		Movie: []tmdb.Genre{{ID: 28, Name: "Action"}},
	}})
	m, _ = drive(t, m, tea.KeyMsg{Type: tea.KeyCtrlG})

	m, cmd := drive(t, m, runes("q"))
	if !m.picker.shown {
		t.Fatal("q closed the overlay (list quit binding leaked through)")
	}
	if cmd != nil {
		if _, isQuit := cmd().(tea.QuitMsg); isQuit {
			t.Fatal("q in overlay quit the program")
		}
	}
}

func mustRender(t *testing.T, m model) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("View panicked: %v", r)
		}
	}()
	if strings.TrimSpace(m.View()) == "" {
		t.Fatal("View produced empty output")
	}
}
