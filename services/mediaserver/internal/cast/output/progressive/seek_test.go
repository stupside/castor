package progressive

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"
)

func seekFrom(t *testing.T, url, npt string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	req.Header.Set(timeSeekHeader, npt)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, body
}

func TestAStreamServedFromItsStartOnlyRefusesASeek(t *testing.T) {
	stream, srv, _, _ := continuing(t)
	if resp, _ := seekFrom(t, srv.URL+"/"+stream.Path(), "npt=60-"); resp.StatusCode != http.StatusNotAcceptable {
		t.Errorf("a seek on a stream nobody can make elsewhere got %d, want 406", resp.StatusCode)
	}
}

func TestASeekServesTheStreamMadeFromThePositionAskedFor(t *testing.T) {
	stream, srv, _, rest := continuing(t)
	var asked time.Duration
	stream.SeekWith(2*time.Minute, func(_ context.Context, at time.Duration) error {
		asked = at
		stream.Restart(at, finished(t, rest))
		return nil
	})

	resp, body := seekFrom(t, srv.URL+"/"+stream.Path(), "npt=0:01:00.5-")
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, rest) {
		t.Fatalf("a seek got %d with %d bytes, want 200 and the %d made from there", resp.StatusCode, len(body), len(rest))
	}
	if asked != 60500*time.Millisecond || stream.Began() != asked {
		t.Errorf("made from %v and began at %v, want both at 1:00.5", asked, stream.Began())
	}
	if got := resp.Header.Get(timeSeekHeader); got != "npt=60.500-120.000/120.000" {
		t.Errorf("%s = %q, want the position served over the runtime", timeSeekHeader, got)
	}
	if handed, _ := stream.Handed(); handed != int64(len(rest)) {
		t.Errorf("handed %d bytes, want only the %d of the stream as it began again", handed, len(rest))
	}

	// Every request after it is served the stream as it began again.
	if _, body := retrieve(t, srv.URL+"/"+stream.Path(), ""); !bytes.Equal(body, rest) {
		t.Errorf("a later request got %d bytes, want the %d made from the seek", len(body), len(rest))
	}
}

func TestASeekPastTheRuntimeIsRefused(t *testing.T) {
	stream, srv, _, rest := continuing(t)
	stream.SeekWith(time.Minute, func(_ context.Context, at time.Duration) error { stream.Restart(at, finished(t, rest)); return nil })
	for _, npt := range []string{"npt=60-", "npt=-", "npt=NaN-", "bytes=0-"} {
		if resp, _ := seekFrom(t, srv.URL+"/"+stream.Path(), npt); resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
			t.Errorf("%q got %d, want 416", npt, resp.StatusCode)
		}
	}
}

func TestAClientOfTheStreamBeforeASeekIsLeftBehind(t *testing.T) {
	stream, srv, own, rest := continuing(t)
	stream.SeekWith(2*time.Minute, func(_ context.Context, at time.Duration) error { stream.Restart(at, finished(t, rest)); return nil })

	tail, err := stream.tailAt(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer tail.Close()
	if _, err := tail.Read(make([]byte, 1024)); err != nil {
		t.Fatal(err)
	}
	seekFrom(t, srv.URL+"/"+stream.Path(), "npt=30-")
	if _, err := tail.Read(make([]byte, len(own))); err != errRestarted {
		t.Errorf("a client of the stream before the seek read on with %v, want it left behind", err)
	}
}

func TestTheStartOfATimeSeekRangeIsReadInSecondsOrAsAClock(t *testing.T) {
	for npt, want := range map[string]time.Duration{
		"npt=12.5-":        12500 * time.Millisecond,
		"npt=0-":           0,
		"npt=1:02:03.250-": time.Hour + 2*time.Minute + 3250*time.Millisecond,
		"npt=90-120.0":     90 * time.Second,
		" npt=0:00:05- ":   5 * time.Second,
	} {
		if got, ok := nptStart(npt); !ok || got != want {
			t.Errorf("nptStart(%q) = %v, %t; want %v", npt, got, ok, want)
		}
	}
	for _, bad := range []string{"", "npt=", "12-", "npt=12", "npt=-5-", "npt=a-", "npt=Inf-"} {
		if _, ok := nptStart(bad); ok {
			t.Errorf("nptStart(%q) was read", bad)
		}
	}
}
