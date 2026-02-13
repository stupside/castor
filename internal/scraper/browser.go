package scraper

import (
	"context"
	"cmp"
	"fmt"
	"log/slog"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/stupside/castor/internal/app"
)

// defaultCollectionWindow is used when CaptureConfig.CollectionWindow is zero.
const defaultCollectionWindow = 3 * time.Second

// streamMIMETypes are MIME types that indicate an HLS or streaming response.
var streamMIMETypes = map[string]bool{
	"audio/mpegurl":                 true,
	"audio/x-mpegurl":               true,
	"application/x-mpegurl":         true,
	"application/vnd.apple.mpegurl": true,
}

// hlsURLPattern matches HTTP(S) URLs containing .m3u8 in console output.
var hlsURLPattern = regexp.MustCompile(`https?://[^\s"'<>]+\.m3u8[^\s"'<>]*`)

// candidate is a captured stream URL with a score for ranking.
type candidate struct {
	rawURL string
	score  int
}

// captureStreamURL navigates to targetURL with a stealth headless browser,
// intercepts network requests and console messages matching the capture
// patterns, dismisses cookie consent, triggers playback, and returns the
// best-ranked stream URL found during a collection window.
func captureStreamURL(ctx context.Context, browserCfg app.BrowserConfig, targetURL string, capture app.CaptureConfig) (string, error) {
	profile := NewProfile()
	slog.Debug("stealth profile generated",
		"ua", profile.UserAgent,
		"platform", profile.Platform,
		"timezone", profile.TimezoneID,
		"screen", fmt.Sprintf("%dx%d", profile.ScreenWidth, profile.ScreenHeight),
		"webgl", profile.WebGLRenderer,
		"hwConcurrency", profile.HardwareConcurrency,
		"deviceMemory", profile.DeviceMemory,
	)

	opts := allocatorOpts(browserCfg, profile)

	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer allocCancel()

	taskCtx, taskCancel := chromedp.NewContext(allocCtx)
	defer taskCancel()
	slog.Debug("browser context created")

	if browserCfg.Timeout > 0 {
		var cancel context.CancelFunc
		taskCtx, cancel = context.WithTimeout(taskCtx, browserCfg.Timeout)
		defer cancel()
		slog.Debug("timeout applied", "timeout", browserCfg.Timeout)
	}

	var mu sync.Mutex
	var candidates []candidate

	firstHit := make(chan struct{}, 1)

	addCandidate := func(u string, source string) {
		mu.Lock()
		defer mu.Unlock()
		if slices.ContainsFunc(candidates, func(c candidate) bool { return c.rawURL == u }) {
			return
		}
		score := rankURL(u)
		candidates = append(candidates, candidate{rawURL: u, score: score})

		slog.Debug("captured candidate stream URL", "url", u, "source", source, "score", score)

		select {
		case firstHit <- struct{}{}:
		default:
		}
	}

	chromedp.ListenTarget(taskCtx, func(ev any) {
		switch e := ev.(type) {
		case *network.EventRequestWillBeSent:
			u := e.Request.URL
			if matchesPattern(u, capture) {
				addCandidate(u, "request")
			}

		case *network.EventResponseReceived:
			mime := strings.ToLower(e.Response.MimeType)
			if streamMIMETypes[mime] {
				addCandidate(e.Response.URL, fmt.Sprintf("mime:%s", mime))
			}

		case *runtime.EventConsoleAPICalled:
			for _, arg := range e.Args {
				val := string(arg.Value)
				val = strings.Trim(val, `"`)
				if matchesPattern(val, capture) {
					addCandidate(val, "console")
					continue
				}
				for _, m := range hlsURLPattern.FindAllString(val, -1) {
					if matchesPattern(m, capture) {
						addCandidate(m, "console-regex")
					}
				}
			}
		}
	})
	slog.Debug("event listeners registered")

	slog.Debug("navigating to target", "url", targetURL)
	err := chromedp.Run(taskCtx,
		runtime.Enable(),
		network.Enable(),
		injectStealth(profile),
		injectCDPStealth(profile),
		chromedp.Navigate(targetURL),
	)
	if err != nil {
		mu.Lock()
		if len(candidates) > 0 {
			best, _ := pickBest(candidates)
			mu.Unlock()
			return best, nil
		}
		mu.Unlock()
		return "", fmt.Errorf("navigating to %s: %w", targetURL, err)
	}
	slog.Debug("navigation completed")
	debugSnapshot(taskCtx, "nav")

	mu.Lock()
	found := len(candidates) > 0
	mu.Unlock()

	if found {
		slog.Debug("stream captured during navigation, skipping interactions")
	} else {
		slog.Debug("starting playback trigger")
		triggerPlayback(taskCtx, profile)
	}

	// Collection window: wait for first hit, then collect more
	collectionWindow := capture.CollectionWindow
	if collectionWindow == 0 {
		collectionWindow = defaultCollectionWindow
	}

	slog.Debug("entering collection window", "window", collectionWindow)

	select {
	case <-firstHit:
		slog.Debug("first stream URL captured, collecting more", "window", collectionWindow)
		timer := time.NewTimer(collectionWindow)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-taskCtx.Done():
		}
	case <-taskCtx.Done():
		mu.Lock()
		if len(candidates) > 0 {
			best, _ := pickBest(candidates)
			mu.Unlock()
			return best, nil
		}
		mu.Unlock()
		return "", fmt.Errorf("timed out waiting for stream URL at %s", targetURL)
	}

	// Return best match
	mu.Lock()
	defer mu.Unlock()
	if len(candidates) == 0 {
		return "", fmt.Errorf("no stream URL captured at %s", targetURL)
	}
	best, _ := pickBest(candidates)
	return best, nil
}

// matchesPattern checks if a URL matches any of the capture patterns.
func matchesPattern(u string, capture app.CaptureConfig) bool {
	stripped := strings.SplitN(u, "?", 2)[0]
	ext := strings.ToLower(path.Ext(stripped))
	if slices.Contains(capture.Extensions, ext) {
		return true
	}
	return slices.ContainsFunc(capture.Substrings, func(sub string) bool {
		return strings.Contains(u, sub)
	})
}

// rankURL assigns a score to a captured URL for quality/variant selection.
// Higher score = more preferred (master playlists over variants).
func rankURL(rawURL string) int {
	score := 0

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return score
	}

	p := strings.ToLower(parsed.Path)

	// Master playlist indicators
	if strings.Contains(p, "master") {
		score += 100
	}
	if strings.Contains(p, "playlist") {
		score += 50
	}

	// Variant indicators
	variantPatterns := []string{
		"/720p/", "/1080p/", "/480p/", "/360p/", "/240p/",
		"_720.", "_1080.", "_480.", "_360.", "_240.",
		"/chunklist", "/media-", "/segment",
	}
	if slices.ContainsFunc(variantPatterns, func(vp string) bool {
		return strings.Contains(p, vp)
	}) {
		score -= 50
	}

	// Shorter paths are slightly preferred (master playlists tend to be shorter)
	if len(p) < 50 {
		score += 10
	}

	return score
}

// pickBest returns the URL with the highest score.
// Returns ("", false) if the slice is empty.
func pickBest(candidates []candidate) (string, bool) {
	if len(candidates) == 0 {
		return "", false
	}
	best := slices.MaxFunc(candidates, func(a, b candidate) int {
		return cmp.Compare(a.score, b.score)
	})
	slog.Debug("selected best stream URL", "url", best.rawURL, "score", best.score, "total_candidates", len(candidates))
	return best.rawURL, true
}
