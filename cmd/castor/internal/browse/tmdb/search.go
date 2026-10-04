package tmdb

import (
	"cmp"
	"context"
	"net/url"

	"golang.org/x/sync/errgroup"
)

// SearchResult is one API result; caller picks title/date pair based on MediaType.
type SearchResult struct {
	ID           int     `json:"id"`
	MediaType    Media   `json:"media_type"`
	Title        string  `json:"title"`
	Name         string  `json:"name"`
	ReleaseDate  string  `json:"release_date"`
	FirstAirDate string  `json:"first_air_date"`
	Overview     string  `json:"overview"`
	VoteAverage  float64 `json:"vote_average"`
	PosterPath   string  `json:"poster_path"`
}

func (r SearchResult) DisplayTitle() string {
	return cmp.Or(r.Title, r.Name)
}

// Year is the 4-digit release/air year, or "" if TMDB stated no date.
func (r SearchResult) Year() string {
	if d := cmp.Or(r.ReleaseDate, r.FirstAirDate); len(d) >= 4 {
		return d[:4]
	}
	return ""
}

// interleave preserves TMDB's per-type ranking better than sorting union by popularity.
func interleave(movies, shows []SearchResult) []SearchResult {
	out := make([]SearchResult, 0, len(movies)+len(shows))
	for i := range max(len(movies), len(shows)) {
		if i < len(movies) {
			out = append(out, movies[i])
		}
		if i < len(shows) {
			out = append(out, shows[i])
		}
	}
	return out
}

// Search uses /search/movie and /search/tv (not /multi which includes people).
func (c *Client) Search(ctx context.Context, query string) ([]SearchResult, error) {
	q := url.Values{"query": {query}, "include_adult": {"false"}}
	return c.interleaved(ctx, q, func(media Media) string { return "/search/" + string(media) })
}

// Trending uses /trending/{movie,tv}/week (not /all/week which includes people).
func (c *Client) Trending(ctx context.Context) ([]SearchResult, error) {
	return c.interleaved(ctx, nil, func(media Media) string { return "/trending/" + string(media) + "/week" })
}

// interleaved asks the movie and the TV endpoint path names at once, and interleaves their results.
func (c *Client) interleaved(ctx context.Context, q url.Values, path func(Media) string) ([]SearchResult, error) {
	var movies, shows []SearchResult
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) { movies, err = c.typed(ctx, path(MediaMovie), q, MediaMovie); return })
	g.Go(func() (err error) { shows, err = c.typed(ctx, path(MediaTV), q, MediaTV); return })
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return interleave(movies, shows), nil
}

// typed stamps media on results from single-type endpoints, since the path implies it.
func (c *Client) typed(ctx context.Context, path string, q url.Values, media Media) ([]SearchResult, error) {
	var resp struct {
		Results []SearchResult `json:"results"`
	}
	if err := c.get(ctx, path, q, &resp); err != nil {
		return nil, err
	}
	for i := range resp.Results {
		resp.Results[i].MediaType = media
	}
	return resp.Results, nil
}
