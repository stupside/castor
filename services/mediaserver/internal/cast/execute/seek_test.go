package execute

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// seekable prepares a cast of a minute-long source whose runtime is declared, on a device with caps.
func seekable(t *testing.T, caps media.Capabilities) (presentation, context.Context) {
	t.Helper()
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	origin := serveHeavy(t, ffmpegPath)
	c := realCast(&fakeDevice{caps: caps}, ffmpegPath, ffprobePath)
	program := programFromStream(t, origin.stream())
	ctx, cancel := context.WithTimeout(t.Context(), castTimeout)
	t.Cleanup(cancel)
	s := testPreparation(ctx, c, recovery.Attempt{Try: 1, Program: program, Origin: source.Origin{Duration: time.Minute}, Fetch: sourceFetchPlan(t, program, testReadDeadline)})
	t.Cleanup(func() { _ = s.releases.release() })
	p, err := s.preparePresentation()
	if err != nil {
		t.Fatalf("preparing: %v", err)
	}
	return p, ctx
}

// firstPicture is when the first picture of a transport stream is presented, in seconds.
func firstPicture(t *testing.T, ffprobePath string, stream []byte) float64 {
	t.Helper()
	file := filepath.Join(t.TempDir(), "served.ts")
	if err := os.WriteFile(file, stream, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(ffprobePath, "-v", "error", "-select_streams", "v", "-read_intervals", "%+#1", "-show_entries", "packet=pts_time", "-of", "csv=p=0", file).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	pts, err := strconv.ParseFloat(strings.Trim(strings.Split(string(out), "\n")[0], " ,"), 64)
	if err != nil {
		t.Fatalf("no picture in what was served: %q", out)
	}
	return pts
}

func fetchFrom(t *testing.T, ctx context.Context, target, npt string, n int) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if npt != "" {
		req.Header.Set("TimeSeekRange.dlna.org", npt)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := make([]byte, n)
	if _, err := io.ReadFull(resp.Body, body); err != nil {
		t.Fatalf("reading what was served: %v", err)
	}
	return resp, body
}

func TestASeekServesTheStreamFromWhereTheDeviceAsked(t *testing.T) {
	for name, caps := range map[string]media.Capabilities{
		"the read's buffer as it is": dlnaLike(),
		"encoders over the read":     ac3Only(),
	} {
		t.Run(name, func(t *testing.T) {
			_, ffprobePath := requireFFmpegTools(t)
			p, ctx := seekable(t, caps)
			if served, _ := p.stream().(Served); served.Runtime == nil || !served.Runtime.TimeSeek {
				t.Fatalf("a cast with a runtime is played as %+v, want a stream served from any time", p.stream())
			}
			_, head := fetchFrom(t, ctx, p.target.String(), "", 200<<10)
			resp, body := fetchFrom(t, ctx, p.target.String(), "npt=30-", 200<<10)
			answer := resp.Header.Get("TimeSeekRange.dlna.org")
			start, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimPrefix(answer, "npt="), "-60.000/60.000"), 64)
			if resp.StatusCode != http.StatusOK || err != nil || start < 29 || start > 30 {
				t.Fatalf("a seek got %s with %q, want 200 and the keyframe at about 30s over the runtime", resp.Status, answer)
			}
			if from, at := firstPicture(t, ffprobePath, head), firstPicture(t, ffprobePath, body); at-from < 29 || at-from > 31 {
				t.Errorf("the seek served pictures from %.2fs into the source, want about 30s", at-from)
			}
		})
	}
}

func TestACastWithoutADeclaredRuntimeIsServedFromItsStartOnly(t *testing.T) {
	p, ctx := prepared(t, dlnaLike())
	if served, _ := p.stream().(Served); served.Runtime != nil {
		t.Errorf("a cast with no runtime is played as %+v, want a stream served from its start only", p.stream())
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.target.String(), nil)
	req.Header.Set("TimeSeekRange.dlna.org", "npt=30-")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotAcceptable {
		t.Errorf("a seek on a cast served from its start only got %s, want 406", resp.Status)
	}
}

func TestASeekReleasesWhatTheSeekBeforeItStarted(t *testing.T) {
	p, ctx := seekable(t, dlnaLike())
	a := p.delivery.rate.(*adjustable)
	fetchFrom(t, ctx, p.target.String(), "npt=20-", 64<<10)
	first := filepath.Join(a.dir, "seek-1"+encoding.SpoolFormat.Extension)
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("the first seek's read: %v", err)
	}
	fetchFrom(t, ctx, p.target.String(), "npt=40-", 64<<10)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if _, err := os.Stat(first); errors.Is(err, fs.ErrNotExist) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the first seek's read is still held after another seek took its place")
		}
	}
}

func TestASeekTheDeviceGaveUpOnChangesNothing(t *testing.T) {
	p, _ := seekable(t, dlnaLike())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := p.delivery.rate.(*adjustable).Seek(ctx, 30*time.Second); err == nil {
		t.Fatal("a seek the device gave up on was made")
	}
	if began := p.delivery.sink.Audience().Began(); began != 0 {
		t.Errorf("the stream began again at %v for a seek the device gave up on", began)
	}
}
