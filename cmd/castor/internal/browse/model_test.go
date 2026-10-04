package browse

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/stupside/castor/cmd/castor/internal/browse/tmdb"
)

func drive(t *testing.T, m model, msg tea.Msg) (model, tea.Cmd) {
	t.Helper()
	tm, cmd := m.Update(msg)
	mm, ok := tm.(model)
	if !ok {
		t.Fatalf("Update returned %T, not model", tm)
	}
	return mm, cmd
}

func runes(s string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: []rune(s)[0], Text: s} }

func fakeResults(n int) []tmdb.SearchResult {
	rs := make([]tmdb.SearchResult, n)
	for i := range rs {
		rs[i] = tmdb.SearchResult{ID: i + 1, MediaType: tmdb.MediaMovie, Title: fmt.Sprintf("Movie %d", i+1), VoteAverage: 7}
	}
	return rs
}

// The list's default "q" quit binding must not leak out of the overlay.
func TestGenreOverlayDoesNotQuitOnQ(t *testing.T) {
	m := newModel(t.Context(), shelf{}, "")
	m, _ = drive(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = drive(t, m, genresLoadedMsg{cat: tmdb.GenreCatalog{
		Movie: []tmdb.Genre{{ID: 28, Name: "Action"}},
	}})
	m, _ = drive(t, m, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})

	m, cmd := drive(t, m, runes("q"))
	if !m.genres.shown {
		t.Fatal("q closed the overlay (list quit binding leaked through)")
	}
	if cmd != nil {
		if _, isQuit := cmd().(tea.QuitMsg); isQuit {
			t.Fatal("q in overlay quit the program")
		}
	}
}

func TestGenreOverlayOpenedBeforeCatalogIsUsable(t *testing.T) {
	m := newModel(t.Context(), shelf{}, "")
	m, _ = drive(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = drive(t, m, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	m, _ = drive(t, m, genresLoadedMsg{cat: tmdb.GenreCatalog{
		Movie: []tmdb.Genre{{ID: 28, Name: "Action"}, {ID: 35, Name: "Comedy"}},
	}})

	if !strings.Contains(m.View().Content, "Action") {
		t.Fatalf("overlay renders no genre rows:\n%s", m.View().Content)
	}
}

func TestGenreCursorClampedOnShorterCatalogue(t *testing.T) {
	m := newModel(t.Context(), shelf{}, "")
	m, _ = drive(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = drive(t, m, genresLoadedMsg{cat: tmdb.GenreCatalog{
		Movie: []tmdb.Genre{{ID: 1, Name: "A"}, {ID: 2, Name: "B"}, {ID: 3, Name: "C"}},
		TV:    []tmdb.Genre{{ID: 100, Name: "T"}},
	}})
	m, _ = drive(t, m, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	m, _ = drive(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m, _ = drive(t, m, tea.KeyPressMsg{Code: tea.KeyDown})

	m, _ = drive(t, m, runes("m"))
	m, _ = drive(t, m, tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	if _, on := m.genres.selected[100]; !on {
		t.Fatal("space toggled nothing after the media switch")
	}
}

func TestClearingQueryClearsTransientStatus(t *testing.T) {
	base := newModel(t.Context(), shelf{}, "")
	base, _ = drive(t, base, tea.WindowSizeMsg{Width: 100, Height: 30})
	base, _ = drive(t, base, topsLoadedMsg{tab: tabTrending, res: fakeResults(3)})

	search := func(t *testing.T, m model) (model, int) {
		t.Helper()
		m, _ = drive(t, m, runes("a"))
		tok := m.queryTok
		m, _ = drive(t, m, searchTickMsg{tok: tok, query: "a"})
		if !m.loading {
			t.Fatal("the debounce tick should have started the search")
		}
		return m, tok
	}

	m, tok := search(t, base)
	m, _ = drive(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	m, _ = drive(t, m, searchDoneMsg{tok: tok, res: fakeResults(9)})
	if m.loading || m.statusLine() != "" {
		t.Fatalf("spinner survived the cleared query: loading=%v status=%q", m.loading, m.statusLine())
	}

	m, tok = search(t, base)
	m, _ = drive(t, m, searchDoneMsg{tok: tok, err: fmt.Errorf("boom")})
	m, _ = drive(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.err != nil || m.statusLine() != "" {
		t.Fatalf("error survived the cleared query: err=%v status=%q", m.err, m.statusLine())
	}
}

func TestReturningFromDrilldownFitsTheTerminal(t *testing.T) {
	const height = 30
	m := newModel(t.Context(), shelf{}, "")
	m, _ = drive(t, m, tea.WindowSizeMsg{Width: 100, Height: height})
	m, _ = drive(t, m, topsLoadedMsg{tab: tabTrending, res: fakeResults(5)})
	m, _ = drive(t, m, tvDoneMsg{tv: &tmdb.TVDetails{
		Name:    "Show",
		Seasons: []tmdb.Season{{SeasonNumber: 1, Name: "One", EpisodeCount: 5, AirDate: "2020-01-01"}},
	}})
	if got := lipgloss.Height(m.View().Content); got > height {
		t.Fatalf("drilldown renders %d rows in a %d-row terminal", got, height)
	}

	// '?' on the drilldown sizes the shared body for the drilldown's chrome.
	m, _ = drive(t, m, runes("?"))
	m, _ = drive(t, m, runes("?"))
	m, _ = drive(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.scr != screenBrowse {
		t.Fatal("esc on seasons should return to browse")
	}
	if got := lipgloss.Height(m.View().Content); got > height {
		t.Fatalf("browse renders %d rows in a %d-row terminal", got, height)
	}
}

// shelf is a catalog that answers at once from a fixed shelf, so a run renders the same every time.
type shelf struct{}

var (
	matrix = tmdb.SearchResult{ID: 603, MediaType: tmdb.MediaMovie, Title: "The Matrix", ReleaseDate: "1999-03-31", Overview: "A hacker learns the truth about his reality.", VoteAverage: 8.2}
	office = tmdb.SearchResult{ID: 2316, MediaType: tmdb.MediaTV, Name: "The Office", FirstAirDate: "2005-03-24", Overview: "A mockumentary about office life.", VoteAverage: 8.6}
)

func (shelf) Search(context.Context, string) ([]tmdb.SearchResult, error) {
	return []tmdb.SearchResult{matrix, office}, nil
}

func (shelf) Trending(context.Context) ([]tmdb.SearchResult, error) {
	return []tmdb.SearchResult{matrix, office}, nil
}

func (shelf) Discover(context.Context, tmdb.DiscoverParams) (tmdb.Page, error) {
	return tmdb.Page{Results: []tmdb.SearchResult{matrix, office}, TotalPages: 1}, nil
}

func (shelf) Genres(context.Context) (tmdb.GenreCatalog, error) {
	return tmdb.GenreCatalog{Movie: []tmdb.Genre{{ID: 28, Name: "Action"}}, TV: []tmdb.Genre{{ID: 35, Name: "Comedy"}}}, nil
}

func (shelf) Details(_ context.Context, media tmdb.Media, _ int) (*tmdb.Details, error) {
	if media == tmdb.MediaTV {
		return &tmdb.Details{Tagline: "Paper is our business.", EpisodeRunTime: []int{22}}, nil
	}
	return &tmdb.Details{Tagline: "Welcome to the Real World.", Runtime: 136, Genres: []tmdb.Genre{{ID: 28, Name: "Action"}}}, nil
}

func (shelf) TV(context.Context, int) (*tmdb.TVDetails, error) {
	return &tmdb.TVDetails{Name: "The Office", Seasons: []tmdb.Season{
		{SeasonNumber: 1, Name: "Season 1", EpisodeCount: 6, AirDate: "2005-03-24"},
		{SeasonNumber: 2, Name: "Season 2", EpisodeCount: 22, AirDate: "2005-09-20"},
	}}, nil
}

func (shelf) Season(context.Context, int, int) (*tmdb.SeasonDetails, error) {
	return &tmdb.SeasonDetails{Episodes: []tmdb.Episode{
		{EpisodeNumber: 1, Name: "Pilot", AirDate: "2005-03-24"},
		{EpisodeNumber: 2, Name: "Diversity Day", AirDate: "2005-03-29"},
	}}, nil
}

func (shelf) Poster(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("the shelf has no posters")
}

// browsing runs the real program at a fixed size and colour profile, as a terminal would.
func browsing(t *testing.T) *teatest.TestModel {
	t.Helper()
	m := newModel(t.Context(), shelf{}, "DLNA  Living room")
	return teatest.NewTestModel(t, m,
		teatest.WithInitialTermSize(110, 32),
		teatest.WithProgramOptions(tea.WithColorProfile(colorprofile.Ascii)),
	)
}

func showing(t *testing.T, tm *teatest.TestModel, text string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool { return bytes.Contains(b, []byte(text)) }, teatest.WithDuration(5*time.Second))
}

func TestEnterOnATrendingMovieCastsIt(t *testing.T) {
	tm := browsing(t)
	showing(t, tm, "Welcome to the Real World.")

	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

	if got, want := tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(model).sel, (Selection{Kind: KindMovie, TMDBID: "603", Title: "The Matrix"}); got == nil || *got != want {
		t.Errorf("selected %+v, want %+v", got, want)
	}
}

func TestAShowIsDrilledIntoUntilAnEpisodeIsChosen(t *testing.T) {
	tm := browsing(t)
	showing(t, tm, "Welcome to the Real World.")

	tm.Send(tea.KeyPressMsg{Code: tea.KeyDown})
	showing(t, tm, "Paper is our business.")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	showing(t, tm, "Season 2")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	showing(t, tm, "Diversity Day")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyDown})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

	want := Selection{Kind: KindEpisode, TMDBID: "2316", Title: "The Office · S01E02 · Diversity Day", Season: 1, Episode: 2}
	if got := tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(model).sel; got == nil || *got != want {
		t.Errorf("selected %+v, want %+v", got, want)
	}
}
