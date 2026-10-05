package extract

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

func TestOnlyAFrameOnTheWebIsOpened(t *testing.T) {
	for src, want := range map[string]bool{
		"https://player.example/embed/1": true,
		"http://player.example/embed/1":  true,
		"javascript:alert(1)":            false,
		"data:text/html,<p>x</p>":        false,
		"file:///etc/passwd":             false,
		"chrome://settings":              false,
		"blob:https://site.example/id":   false,
	} {
		if got := webPage(src); got != want {
			t.Errorf("webPage(%q) = %v, want %v", src, got, want)
		}
	}
}

func TestVisibleIframeNavigationWaitsForItsDocumentAndPreservesReferrer(t *testing.T) {
	chrome := os.Getenv("CASTOR_TEST_CHROME")
	if chrome == "" {
		t.Skip("set CASTOR_TEST_CHROME to exercise real browser navigation")
	}
	requests := make(chan string, 8)
	ts := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/parent":
			fmt.Fprint(w, `<iframe src="about:blank" style="width:1000px;height:600px"></iframe><iframe src="/hidden" style="visibility:hidden;width:1000px;height:600px"></iframe><iframe src="/child" style="position:fixed;top:0;left:0;width:300px;height:200px"></iframe>`)
		case "/child":
			requests <- r.Referer()
			fmt.Fprint(w, `<body id="child">child</body>`)
		default:
			fmt.Fprint(w, `<body>hidden</body>`)
		}
	}))
	ts.Start()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	s, err := newSession(ctx, BrowserConfig{ChromePath: chrome, Headless: true, Timeout: 10 * time.Second}, ts.URL+"/parent")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	t.Run("canvas export preserves source pixels", func(t *testing.T) {
		var preserved bool
		if err := chromedp.Run(s.ctx, chromedp.Evaluate(`(() => {
			const canvas = document.createElement('canvas');
			canvas.width = 16; canvas.height = 1;
			const ctx = canvas.getContext('2d');
			ctx.fillStyle = '#808080'; ctx.fillRect(0, 0, 16, 1);
			const before = Array.from(ctx.getImageData(0, 0, 16, 1).data).join(',');
			canvas.toDataURL(); canvas.toDataURL();
			return before === Array.from(ctx.getImageData(0, 0, 16, 1).data).join(',');
		})()`, &preserved)); err != nil {
			t.Fatal(err)
		}
		if !preserved {
			t.Fatal("export changed the canvas used by the page")
		}
	})
	t.Run("notification permission supports event listeners", func(t *testing.T) {
		var supported bool
		if err := chromedp.Run(s.ctx, chromedp.Evaluate(`navigator.permissions.query({name:'notifications'}).then(status => {
			status.addEventListener('change', () => {}); return true;
		})`, &supported, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) })); err != nil {
			t.Fatal(err)
		}
		if !supported {
			t.Fatal("permission query no longer supplies a usable PermissionStatus")
		}
	})
	var src string
	if err := chromedp.Run(s.ctx, chromedp.Evaluate(iframeSrcJS, &src)); err != nil {
		t.Fatal(err)
	}
	if src != ts.URL+"/child" {
		t.Fatalf("visible iframe selection = %q", src)
	}
	if err := openFrame(s.ctx, src, ts.URL+"/parent"); err != nil {
		t.Fatal(err)
	}
	var body string
	if err := chromedp.Run(s.ctx, chromedp.Text("body", &body)); err != nil || body != "child" {
		t.Fatalf("frame navigation returned before new body: %q, %v", body, err)
	}
	for range 2 { // The embedded load, then opening that frame as the page.
		select {
		case referer := <-requests:
			if referer != ts.URL+"/parent" {
				t.Errorf("frame Referer = %q", referer)
			}
		case <-ctx.Done():
			t.Fatal("frame request missing")
		}
	}
}
