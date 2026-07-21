// Package tmdb provides a minimal read-only client for The Movie Database v3 API.
// Only the endpoints needed by the browse subcommand are implemented.
package tmdb

import (
	"bufio"
	"cmp"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

const (
	defaultBase = "https://api.themoviedb.org/3"
	imageBase   = "https://image.tmdb.org/t/p/"

	// MediaMovie / MediaTV are the two castable TMDB media types. They double
	// as URL path segments (/movie/…, /tv/…) and as the media_type discriminator
	// on mixed result sets.
	MediaMovie = "movie"
	MediaTV    = "tv"
)

// Client is a TMDB v3 API client.
type Client struct {
	apiKey string
	base   string
	http   *http.Client
}

// SearchResult is one row of /search/multi. Movie / TV use different title
// and date fields, so both pairs are exposed and the caller picks based on
// MediaType.
type SearchResult struct {
	ID           int     `json:"id"`
	MediaType    string  `json:"media_type"` // "movie" | "tv" | "person"
	Title        string  `json:"title"`
	Name         string  `json:"name"`
	ReleaseDate  string  `json:"release_date"`
	FirstAirDate string  `json:"first_air_date"`
	Overview     string  `json:"overview"`
	VoteAverage  float64 `json:"vote_average"`
	PosterPath   string  `json:"poster_path"`
	GenreIDs     []int   `json:"genre_ids"`
}

// PosterURL returns a full URL for the poster at the given TMDB size
// (w92, w154, w185, w342, w500, original). Empty string if no poster.
func (r SearchResult) PosterURL(size string) string {
	if r.PosterPath == "" {
		return ""
	}
	return imageBase + size + r.PosterPath
}

// DisplayTitle returns the user-facing title regardless of MediaType.
func (r SearchResult) DisplayTitle() string {
	return cmp.Or(r.Title, r.Name)
}

// Year returns the 4-digit release/air year, or "" if unavailable.
func (r SearchResult) Year() string {
	d := cmp.Or(r.ReleaseDate, r.FirstAirDate)
	if len(d) >= 4 {
		return d[:4]
	}
	return ""
}

// Genre is a TMDB genre. IDs are namespaced per media type — movie "Action"
// (28) and TV "Action & Adventure" (10759) are distinct — so genres are always
// carried together with their media type.
type Genre struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// GenreCatalog holds the genre lists for both castable media types.
type GenreCatalog struct {
	Movie []Genre
	TV    []Genre
}

// For returns the genre list for a media type (MediaMovie / MediaTV).
func (c GenreCatalog) For(mediaType string) []Genre {
	if mediaType == MediaTV {
		return c.TV
	}
	return c.Movie
}

// Details is the superset of /movie/{id} and /tv/{id} fields the metadata
// panel renders. Movie-only and TV-only fields sit side by side; the unused
// side stays zero for a given media type.
type Details struct {
	Tagline        string  `json:"tagline"`
	Runtime        int     `json:"runtime"`          // movie: minutes
	EpisodeRunTime []int   `json:"episode_run_time"` // tv: per-episode minutes
	Genres         []Genre `json:"genres"`
	Credits        struct {
		Cast []struct {
			Name string `json:"name"`
		} `json:"cast"`
	} `json:"credits"`
}

// RuntimeMinutes returns the best single runtime figure: the movie runtime, or
// a TV show's typical episode runtime, or 0 if unknown.
func (d *Details) RuntimeMinutes() int {
	if d.Runtime > 0 {
		return d.Runtime
	}
	if len(d.EpisodeRunTime) > 0 {
		return d.EpisodeRunTime[0]
	}
	return 0
}

// GenreNames returns the genre display names in TMDB order.
func (d *Details) GenreNames() []string {
	names := make([]string, len(d.Genres))
	for i, g := range d.Genres {
		names[i] = g.Name
	}
	return names
}

// TopCast returns up to n billed cast member names.
func (d *Details) TopCast(n int) []string {
	names := make([]string, 0, n)
	for _, member := range d.Credits.Cast {
		if len(names) == n {
			break
		}
		if member.Name != "" {
			names = append(names, member.Name)
		}
	}
	return names
}

// TVDetails is /tv/{id}.
type TVDetails struct {
	Name    string   `json:"name"`
	Seasons []Season `json:"seasons"`
}

// Season is a season summary from TVDetails.
type Season struct {
	SeasonNumber int    `json:"season_number"`
	Name         string `json:"name"`
	EpisodeCount int    `json:"episode_count"`
	AirDate      string `json:"air_date"`
}

// Episode is one entry of /tv/{id}/season/{n}.
type Episode struct {
	EpisodeNumber int    `json:"episode_number"`
	Name          string `json:"name"`
	Overview      string `json:"overview"`
	AirDate       string `json:"air_date"`
}

// SeasonDetails is /tv/{id}/season/{n}.
type SeasonDetails struct {
	Episodes []Episode `json:"episodes"`
}

// New builds a Client. timeout==0 falls back to 10s.
func New(apiKey string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	// A tuned transport keeps connections warm across the many small calls the
	// browse TUI makes (tops, search, per-hover details, discover pages).
	transport := &http.Transport{
		MaxIdleConns:        32,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     60 * time.Second,
		ForceAttemptHTTP2:   true,
	}
	return &Client{
		apiKey: apiKey,
		base:   defaultBase,
		http:   &http.Client{Timeout: timeout, Transport: transport},
	}
}

// SearchMulti searches across movies, TV shows, and people. People are
// filtered out here since they are never castable.
func (c *Client) SearchMulti(ctx context.Context, query string) ([]SearchResult, error) {
	var resp struct {
		Results []SearchResult `json:"results"`
	}
	q := url.Values{}
	q.Set("query", query)
	q.Set("include_adult", "false")
	if err := c.get(ctx, "/search/multi", q, &resp); err != nil {
		return nil, err
	}
	return castable(resp.Results), nil
}

// TV fetches /tv/{id}.
func (c *Client) TV(ctx context.Context, id int) (*TVDetails, error) {
	var d TVDetails
	if err := c.get(ctx, "/tv/"+strconv.Itoa(id), nil, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// Details fetches /{mediaType}/{id} with credits folded into the same round
// trip via append_to_response.
func (c *Client) Details(ctx context.Context, mediaType string, id int) (*Details, error) {
	q := url.Values{}
	q.Set("append_to_response", "credits")
	var d Details
	if err := c.get(ctx, "/"+mediaType+"/"+strconv.Itoa(id), q, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// Genres fetches the movie and TV genre catalogs concurrently.
func (c *Client) Genres(ctx context.Context) (GenreCatalog, error) {
	var cat GenreCatalog
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) { cat.Movie, err = c.genreList(ctx, MediaMovie); return })
	g.Go(func() (err error) { cat.TV, err = c.genreList(ctx, MediaTV); return })
	if err := g.Wait(); err != nil {
		return GenreCatalog{}, err
	}
	return cat, nil
}

func (c *Client) genreList(ctx context.Context, mediaType string) ([]Genre, error) {
	var resp struct {
		Genres []Genre `json:"genres"`
	}
	if err := c.get(ctx, "/genre/"+mediaType+"/list", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Genres, nil
}

// Sort selects the ordering for Discover.
type Sort int

const (
	SortPopularity Sort = iota
	SortRating
	SortNewest
)

// Label is the user-facing name of a sort.
func (s Sort) Label() string {
	switch s {
	case SortRating:
		return "Rating"
	case SortNewest:
		return "Newest"
	default:
		return "Popularity"
	}
}

// Sorts is the ordered set of sorts the UI cycles through.
var Sorts = []Sort{SortPopularity, SortRating, SortNewest}

// DiscoverParams drives a /discover query.
type DiscoverParams struct {
	MediaType string // MediaMovie | MediaTV
	GenreIDs  []int  // AND-combined
	Sort      Sort
	Page      int // 1-based; 0 is treated as 1
}

// Page is one page of discover results plus the paging cursor, so callers can
// tell whether more pages remain without a second request.
type Page struct {
	Results    []SearchResult
	Page       int
	TotalPages int
}

// HasMore reports whether a page after this one exists.
func (p Page) HasMore() bool { return p.Page < p.TotalPages }

// Discover runs /discover/{movie,tv} with a genre filter and sort. MediaType is
// stamped onto every result since /discover responses omit media_type.
func (c *Client) Discover(ctx context.Context, p DiscoverParams) (Page, error) {
	q := url.Values{}
	q.Set("include_adult", "false")
	q.Set("page", strconv.Itoa(max(p.Page, 1)))
	q.Set("sort_by", sortBy(p.Sort, p.MediaType))

	if p.MediaType == MediaMovie {
		q.Set("include_video", "false")
	}
	if len(p.GenreIDs) > 0 {
		ids := make([]string, len(p.GenreIDs))
		for i, id := range p.GenreIDs {
			ids[i] = strconv.Itoa(id)
		}
		q.Set("with_genres", strings.Join(ids, ",")) // comma = AND
	}
	switch p.Sort {
	case SortRating:
		// Rating without a vote floor surfaces obscure titles with a single
		// 10/10 vote; require a meaningful sample.
		q.Set("vote_count.gte", "300")
	case SortNewest:
		// Exclude unreleased future-dated entries that otherwise dominate a
		// date-descending sort.
		q.Set(dateField(p.MediaType)+".lte", today())
	}

	var resp struct {
		Page       int            `json:"page"`
		TotalPages int            `json:"total_pages"`
		Results    []SearchResult `json:"results"`
	}
	if err := c.get(ctx, "/discover/"+p.MediaType, q, &resp); err != nil {
		return Page{}, err
	}
	for i := range resp.Results {
		resp.Results[i].MediaType = p.MediaType
	}
	return Page{Results: resp.Results, Page: resp.Page, TotalPages: resp.TotalPages}, nil
}

func sortBy(s Sort, mediaType string) string {
	switch s {
	case SortRating:
		return "vote_average.desc"
	case SortNewest:
		return dateField(mediaType) + ".desc"
	default:
		return "popularity.desc"
	}
}

func dateField(mediaType string) string {
	if mediaType == MediaTV {
		return "first_air_date"
	}
	return "primary_release_date"
}

func today() string { return time.Now().UTC().Format(time.DateOnly) }

// Trending returns the week's trending movies and TV shows mixed.
func (c *Client) Trending(ctx context.Context) ([]SearchResult, error) {
	return c.list(ctx, "/trending/all/week", "")
}

// PopularMovies returns /movie/popular. MediaType is filled in as "movie".
func (c *Client) PopularMovies(ctx context.Context) ([]SearchResult, error) {
	return c.list(ctx, "/movie/popular", MediaMovie)
}

// PopularTV returns /tv/popular. MediaType is filled in as "tv".
func (c *Client) PopularTV(ctx context.Context) ([]SearchResult, error) {
	return c.list(ctx, "/tv/popular", MediaTV)
}

// TopRatedMovies returns /movie/top_rated.
func (c *Client) TopRatedMovies(ctx context.Context) ([]SearchResult, error) {
	return c.list(ctx, "/movie/top_rated", MediaMovie)
}

// TopRatedTV returns /tv/top_rated.
func (c *Client) TopRatedTV(ctx context.Context) ([]SearchResult, error) {
	return c.list(ctx, "/tv/top_rated", MediaTV)
}

// list is the shared "paged results" GET. forceType is "" for /trending
// (whose response already includes media_type) and "movie"/"tv" otherwise.
func (c *Client) list(ctx context.Context, path, forceType string) ([]SearchResult, error) {
	var resp struct {
		Results []SearchResult `json:"results"`
	}
	if err := c.get(ctx, path, nil, &resp); err != nil {
		return nil, err
	}
	if forceType != "" {
		for i := range resp.Results {
			resp.Results[i].MediaType = forceType
		}
		return resp.Results, nil
	}
	return castable(resp.Results), nil
}

// castable filters a result set to the two castable media types in place.
func castable(rs []SearchResult) []SearchResult {
	out := rs[:0]
	for _, r := range rs {
		if r.MediaType == MediaMovie || r.MediaType == MediaTV {
			out = append(out, r)
		}
	}
	return out
}

// Season fetches /tv/{tvID}/season/{seasonNumber}.
func (c *Client) Season(ctx context.Context, tvID, seasonNumber int) (*SeasonDetails, error) {
	var d SeasonDetails
	path := "/tv/" + strconv.Itoa(tvID) + "/season/" + strconv.Itoa(seasonNumber)
	if err := c.get(ctx, path, nil, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func (c *Client) get(ctx context.Context, path string, extra url.Values, out any) error {
	if extra == nil {
		extra = url.Values{}
	}
	extra.Set("api_key", c.apiKey)
	u := c.base + path + "?" + extra.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("tmdb: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("tmdb: %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("tmdb: %s: status %d", path, resp.StatusCode)
	}

	// Detect gzip by content rather than by the Content-Encoding header —
	// TMDB / its CDN sometimes returns a gzip body without a matching
	// header, which defeats net/http's transparent decompression. Peeking
	// the first 2 bytes for the gzip magic (\x1f\x8b) is reliable and
	// doesn't consume them.
	br := bufio.NewReader(resp.Body)
	var body io.Reader = br
	if peek, _ := br.Peek(2); len(peek) == 2 && peek[0] == 0x1f && peek[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return fmt.Errorf("tmdb: gunzip %s: %w", path, err)
		}
		defer gz.Close()
		body = gz
	}

	if err := json.NewDecoder(body).Decode(out); err != nil {
		return fmt.Errorf("tmdb: decode %s: %w", path, err)
	}
	return nil
}
