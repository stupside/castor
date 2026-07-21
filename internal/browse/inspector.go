package browse

import (
	"context"
	"fmt"
	"image/color"
	"net/http"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/eliukblau/pixterm/pkg/ansimage"

	"github.com/stupside/castor/internal/browse/tmdb"
)

// hoverDebounce collapses a burst of cursor movement into a single asset fetch
// for the row the cursor lands on, so scrolling a long list doesn't fire a
// request per row.
const hoverDebounce = 120 * time.Millisecond

// inspector is the right-hand panel. It owns the lazily-fetched poster and
// rich-metadata caches for the browsed catalogue, debounces loading to the
// settled selection, and renders the poster + metadata column. The list side
// only tells it which result is selected; everything else is private.
type inspector struct {
	ctx    context.Context
	client *tmdb.Client
	styles styles

	posters       map[string]string        // posterPath -> rendered ANSI
	details       map[string]*tmdb.Details // detailKey -> details
	posterPending string
	detailPending map[string]bool
	tok           int // debounce token; only the latest hover settles
}

func newInspector(ctx context.Context, client *tmdb.Client, st styles) inspector {
	return inspector{
		ctx:           ctx,
		client:        client,
		styles:        st,
		posters:       map[string]string{},
		details:       map[string]*tmdb.Details{},
		detailPending: map[string]bool{},
	}
}

// hover schedules a debounced load for whatever is selected once movement
// settles. Callers invoke it on every selection change; only the final tick
// survives the token check.
func (in *inspector) hover() tea.Cmd {
	in.tok++
	return hoverSettleCmd(in.tok)
}

// update consumes the inspector's own async messages. sel is the currently
// selected result (nil when the list is empty), used to resolve a settled
// hover into a fetch.
func (in *inspector) update(msg tea.Msg, sel *tmdb.SearchResult) tea.Cmd {
	switch msg := msg.(type) {
	case hoverSettleMsg:
		if msg.tok != in.tok || sel == nil {
			return nil
		}
		return in.load(*sel)
	case posterReadyMsg:
		if msg.err == nil && msg.ansi != "" {
			in.posters[msg.posterPath] = msg.ansi
		}
		if msg.posterPath == in.posterPending {
			in.posterPending = ""
		}
		return nil
	case detailsReadyMsg:
		delete(in.detailPending, msg.key)
		if msg.err == nil {
			in.details[msg.key] = msg.d
		}
		return nil
	}
	return nil
}

// load fetches poster + details for r, deduped against cache and in-flight
// requests.
func (in *inspector) load(r tmdb.SearchResult) tea.Cmd {
	return tea.Batch(in.loadPoster(r), in.loadDetails(r))
}

func (in *inspector) loadPoster(r tmdb.SearchResult) tea.Cmd {
	if r.PosterPath == "" || in.posterPending == r.PosterPath {
		return nil
	}
	if _, ok := in.posters[r.PosterPath]; ok {
		return nil
	}
	in.posterPending = r.PosterPath
	return fetchPosterCmd(in.ctx, r.PosterURL("w500"), r.PosterPath, posterCols, posterRows)
}

func (in *inspector) loadDetails(r tmdb.SearchResult) tea.Cmd {
	key := detailKey(r.MediaType, r.ID)
	if _, done := in.details[key]; done {
		return nil
	}
	if in.detailPending[key] {
		return nil
	}
	in.detailPending[key] = true
	return detailsCmd(in.ctx, in.client, r.MediaType, r.ID)
}

// view renders the poster + metadata column for sel, clamped to exactly height
// rows. A nil selection yields a blank column so the layout never shifts.
//
// The poster string is either a stream of per-cell true-color ANSI half-block
// escapes (pixterm fallback) or a single Kitty/iTerm/Sixel image escape
// sequence padded to posterRows lines — in BOTH cases we deliberately do NOT
// pass it through lipgloss's Width/Height/Render path: lipgloss rewrites ANSI
// runs and would strip per-pixel colour codes from pixterm, and it would
// (worse) split the inline-image control sequence and corrupt it.
func (in inspector) view(sel *tmdb.SearchResult, height int) string {
	if sel == nil {
		return blankRect(posterCols, height)
	}
	r := *sel

	poster := blankRect(posterCols, posterRows)
	if ansi, ok := in.posters[r.PosterPath]; ok && r.PosterPath != "" {
		poster = ansi
	}

	title := r.DisplayTitle()
	if y := r.Year(); y != "" {
		title = fmt.Sprintf("%s (%s)", title, y)
	}
	title = truncate(title, posterCols)

	d := in.details[detailKey(r.MediaType, r.ID)]
	info, tagline, cast := metaLines(r, d, posterCols)

	// The poster spans posterRows; the metadata lines below it are fixed, and
	// the overview flexes into whatever vertical space remains.
	meta := []string{"", in.styles.MetaTitle.Render(title)}
	if info != "" {
		meta = append(meta, in.styles.Muted.Render(info))
	}
	if tagline != "" {
		meta = append(meta, in.styles.Tagline.Render(tagline))
	}
	meta = append(meta, "") // spacer before the overview

	castLines := 0
	if cast != "" {
		castLines = 1
	}
	overviewH := max(height-posterRows-len(meta)-castLines, 0)
	if overviewH > 0 {
		meta = append(meta, in.styles.Overview.MaxHeight(overviewH).Render(r.Overview))
	}
	if cast != "" {
		meta = append(meta, in.styles.Muted.Render(cast))
	}
	return clampRows(poster+"\n"+strings.Join(meta, "\n"), height)
}

// ---------------------------------------------------------------- messages + cmds

type posterReadyMsg struct {
	posterPath string
	ansi       string
	err        error
}

type detailsReadyMsg struct {
	key string
	d   *tmdb.Details
	err error
}

type hoverSettleMsg struct{ tok int }

func hoverSettleCmd(tok int) tea.Cmd {
	return tea.Tick(hoverDebounce, func(time.Time) tea.Msg { return hoverSettleMsg{tok: tok} })
}

func detailsCmd(ctx context.Context, c *tmdb.Client, mediaType string, id int) tea.Cmd {
	key := detailKey(mediaType, id)
	return func() tea.Msg {
		d, err := c.Details(ctx, mediaType, id)
		return detailsReadyMsg{key: key, d: d, err: err}
	}
}

func detailKey(mediaType string, id int) string { return mediaType + ":" + strconv.Itoa(id) }

// fetchPosterCmd downloads the poster at url and renders it to a string of ANSI
// escapes sized to (cols × rows) terminal cells. Half-block rendering shows 2
// vertical pixels per cell, so we ask ansimage for 2*rows pixels of height.
func fetchPosterCmd(ctx context.Context, url, posterPath string, cols, rows int) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(c, http.MethodGet, url, nil)
		if err != nil {
			return posterReadyMsg{posterPath: posterPath, err: err}
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return posterReadyMsg{posterPath: posterPath, err: err}
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return posterReadyMsg{posterPath: posterPath, err: fmt.Errorf("poster: status %d", resp.StatusCode)}
		}

		// NoDithering uses true-color ▀ glyphs (the highest-quality mode this
		// lib offers); ScaleModeResize stretches to exactly fit our reserved
		// footprint so the layout never shifts.
		img, err := ansimage.NewScaledFromReader(
			resp.Body,
			rows*2, cols,
			color.Transparent,
			ansimage.ScaleModeResize,
			ansimage.NoDithering,
		)
		if err != nil {
			return posterReadyMsg{posterPath: posterPath, err: err}
		}
		return posterReadyMsg{posterPath: posterPath, ansi: img.Render()}
	}
}

// ---------------------------------------------------------------- render helpers

// metaLines renders the enriched info + tagline + cast lines, each clamped to
// width. Rating is always available from the list row; runtime/genres/cast fill
// in once details arrive.
func metaLines(r tmdb.SearchResult, d *tmdb.Details, width int) (info, tagline, cast string) {
	var parts []string
	if r.VoteAverage > 0 {
		parts = append(parts, fmt.Sprintf("★ %.1f", r.VoteAverage))
	}
	if d != nil {
		if rt := formatRuntime(d.RuntimeMinutes()); rt != "" {
			parts = append(parts, rt)
		}
		if names := d.GenreNames(); len(names) > 0 {
			parts = append(parts, strings.Join(names, ", "))
		}
	}
	info = truncate(strings.Join(parts, " · "), width)

	if d != nil {
		tagline = truncate(d.Tagline, width)
		if c := d.TopCast(3); len(c) > 0 {
			cast = truncate("With "+strings.Join(c, ", "), width)
		}
	}
	return info, tagline, cast
}

func formatRuntime(minutes int) string {
	if minutes <= 0 {
		return ""
	}
	h, m := minutes/60, minutes%60
	switch {
	case h > 0 && m > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case h > 0:
		return fmt.Sprintf("%dh", h)
	default:
		return fmt.Sprintf("%dm", m)
	}
}

// truncate clamps s to at most width runes, appending an ellipsis when cut.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return string(r[:width-1]) + "…"
}

// blankRect is a w×h block of spaces, used to hold the poster column's
// footprint before its image arrives.
func blankRect(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	line := strings.Repeat(" ", w)
	rows := make([]string, h)
	for i := range rows {
		rows[i] = line
	}
	return strings.Join(rows, "\n")
}

// clampRows forces s to exactly n lines, truncating or padding with blanks.
func clampRows(s string, n int) string {
	if n <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}
