package tmdb

import (
	"slices"
	"testing"
)

func TestSortBy(t *testing.T) {
	cases := []struct {
		sort      Sort
		mediaType string
		want      string
	}{
		{SortPopularity, MediaMovie, "popularity.desc"},
		{SortPopularity, MediaTV, "popularity.desc"},
		{SortRating, MediaMovie, "vote_average.desc"},
		{SortNewest, MediaMovie, "primary_release_date.desc"},
		{SortNewest, MediaTV, "first_air_date.desc"},
	}
	for _, c := range cases {
		if got := sortBy(c.sort, c.mediaType); got != c.want {
			t.Errorf("sortBy(%v,%q) = %q, want %q", c.sort, c.mediaType, got, c.want)
		}
	}
}

func TestSortLabel(t *testing.T) {
	for _, s := range Sorts {
		if s.Label() == "" {
			t.Errorf("sort %d has empty label", s)
		}
	}
	if SortRating.Label() != "Rating" {
		t.Errorf("SortRating label = %q", SortRating.Label())
	}
}

func TestPageHasMore(t *testing.T) {
	if !(Page{Page: 1, TotalPages: 3}).HasMore() {
		t.Error("page 1 of 3 should have more")
	}
	if (Page{Page: 3, TotalPages: 3}).HasMore() {
		t.Error("page 3 of 3 should not have more")
	}
}

func TestGenreCatalogFor(t *testing.T) {
	cat := GenreCatalog{
		Movie: []Genre{{ID: 28, Name: "Action"}},
		TV:    []Genre{{ID: 10759, Name: "Action & Adventure"}},
	}
	if got := cat.For(MediaMovie); len(got) != 1 || got[0].ID != 28 {
		t.Errorf("For(movie) = %+v", got)
	}
	if got := cat.For(MediaTV); len(got) != 1 || got[0].ID != 10759 {
		t.Errorf("For(tv) = %+v", got)
	}
}

func TestDetailsRuntimeMinutes(t *testing.T) {
	if got := (&Details{Runtime: 128}).RuntimeMinutes(); got != 128 {
		t.Errorf("movie runtime = %d", got)
	}
	if got := (&Details{EpisodeRunTime: []int{42, 45}}).RuntimeMinutes(); got != 42 {
		t.Errorf("tv runtime = %d", got)
	}
	if got := (&Details{}).RuntimeMinutes(); got != 0 {
		t.Errorf("empty runtime = %d", got)
	}
}

func TestDetailsGenreNames(t *testing.T) {
	d := &Details{Genres: []Genre{{ID: 28, Name: "Action"}, {ID: 878, Name: "Science Fiction"}}}
	if got := d.GenreNames(); !slices.Equal(got, []string{"Action", "Science Fiction"}) {
		t.Errorf("GenreNames = %v", got)
	}
}

func TestDetailsTopCast(t *testing.T) {
	var d Details
	d.Credits.Cast = []struct {
		Name string `json:"name"`
	}{
		{Name: "A"}, {Name: ""}, {Name: "B"}, {Name: "C"}, {Name: "D"},
	}
	got := d.TopCast(3)
	if !slices.Equal(got, []string{"A", "B", "C"}) {
		t.Errorf("TopCast(3) = %v, want [A B C] (empties skipped)", got)
	}
}

func TestSearchResultTitleAndYear(t *testing.T) {
	movie := SearchResult{Title: "Dune", ReleaseDate: "2021-10-22"}
	if movie.DisplayTitle() != "Dune" || movie.Year() != "2021" {
		t.Errorf("movie = %q %q", movie.DisplayTitle(), movie.Year())
	}
	tv := SearchResult{Name: "Severance", FirstAirDate: "2022-02-18"}
	if tv.DisplayTitle() != "Severance" || tv.Year() != "2022" {
		t.Errorf("tv = %q %q", tv.DisplayTitle(), tv.Year())
	}
	if got := (SearchResult{}).Year(); got != "" {
		t.Errorf("empty year = %q", got)
	}
}

func TestCastableFiltersPeople(t *testing.T) {
	in := []SearchResult{
		{ID: 1, MediaType: MediaMovie},
		{ID: 2, MediaType: "person"},
		{ID: 3, MediaType: MediaTV},
	}
	got := castable(in)
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 3 {
		t.Errorf("castable dropped wrong rows: %+v", got)
	}
}
