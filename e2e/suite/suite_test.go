// Package suite is the e2e framework's composition root, the only place strategies are listed, and runs every case in cases/:
// an origin streams the case's spec, castor casts it to a fake receiver on the device's own protocol, and the judge holds
// what was played to the case. Nothing here imports castor; its binary is the only boundary crossed.
package suite

import (
	"cmp"
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/stupside/castor/e2e/judge"
	"github.com/stupside/castor/e2e/origin"
	"github.com/stupside/castor/e2e/receiver"
)

func TestEveryCaseCastsWhatItsReceiverCanPlay(t *testing.T) {
	tools := findTools(t)
	files, err := filepath.Glob("cases/*.yaml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no cases found in cases/ (%v)", err)
	}
	for _, file := range files {
		t.Run(strings.TrimSuffix(filepath.Base(file), ".yaml"), func(t *testing.T) {
			t.Parallel()
			s := read(t, file)
			layouts, err := s.layouts()
			if err != nil {
				t.Fatalf("%s: %v", file, err)
			}
			for _, layout := range layouts {
				t.Run(layout.Name(), func(t *testing.T) {
					t.Parallel()
					// Each cast binds its own strategies, since an origin's behaviours and a device keep state.
					p, err := s.resolve()
					if err != nil {
						t.Fatalf("%s: %v", file, err)
					}
					cast(t, tools, p, layout)
				})
			}
		})
	}
}

const (
	// castTimeout bounds one cast, well past the longest verdict castor waits for (two stall windows).
	castTimeout = 8 * time.Minute
	// handOff is how long a hand-off may land after castor exits: a detached Cast LOAD is still in flight.
	handOff = 10 * time.Second
)

// casting holds the casts in flight to half the cores: each runs several processes in real time, and a host too loaded to keep up with an origin loses media where it restarts.
var casting = semaphore.NewWeighted(int64(max(1, runtime.NumCPU()/2)))

// cast runs one case under one layout and judges what the receiver played.
func cast(t *testing.T, tools receiver.Tools, p plan, layout topology) {
	if err := casting.Acquire(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	defer casting.Release(1)
	src := origin.Start(t, tools.FFmpeg, p.stream, p.behaviours)
	session := receiver.NewSession(t, receiver.Setup{Tools: tools, Players: players, Viewer: p.viewer})
	endpoint, err := p.device.Start(t, session)
	if err != nil {
		t.Fatal(err)
	}
	invocation := p.command.Invoke(t, src)
	doc := configDoc(t, p.castor, invocation.Config, p.device.Type(), endpoint.Host)

	ctx, cancel := context.WithTimeout(t.Context(), castTimeout)
	defer cancel()
	out, castErr := layout.Cast(t, ctx, p.carrier, doc, invocation.Args)
	// Read now: waiting on the receiver below can outlast the deadline castor itself met.
	exited, killed := time.Now(), ctx.Err() != nil
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("castor's output:\n%s", out)
		}
	})

	got, handed := session.Received(t, handOff)
	e := judge.Evidence{
		Origin: src, Endpoint: endpoint, Viewer: p.viewer, Received: got, Handed: handed,
		CastErr: castErr, Exited: exited, Killed: killed, Ceiling: p.ceiling,
	}
	played := got.Played
	t.Logf("castor exited %v; handed %q (%s): %s %dp %d-bit %s, %s %dch, %v",
		castErr, got.URL, got.ContentType, played.Video, played.Height, played.Depth, cmp.Or(played.Transfer, "untagged"),
		played.Audio, played.Channels, played.Duration)
	for _, check := range slices.Concat([]judge.Check{p.outcome}, p.checks, invocation.Checks) {
		for _, failure := range check.Judge(e) {
			t.Errorf("%s: %s", check.Name(), failure)
		}
	}
}

func findTools(t *testing.T) receiver.Tools {
	t.Helper()
	if testing.Short() {
		t.Skip("every case casts in real time; skipped in -short mode")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatalf("this suite drives real ffmpeg: %v", err)
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Fatalf("this suite drives real ffprobe: %v", err)
	}
	return receiver.Tools{FFmpeg: ffmpeg, FFprobe: ffprobe}
}
