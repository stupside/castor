package recovery

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

var errExited = errors.New("encoder: signal: segmentation fault")

// encodeFailedAt is an encode that failed after played of playback, or before playback when zero.
func encodeFailedAt(played time.Duration, hardware bool) Outcome {
	if played == 0 {
		return Outcome{Err: errExited, Evidence: Evidence{Reached: health.Opening, EncodeErr: errExited, Hardware: hardware}}
	}
	return Outcome{Err: errExited, Evidence: Evidence{Reached: health.Playing, EncodeErr: errExited, Hardware: hardware, Vitals: playedFor(played)}}
}

// wedgedAt is an encoder that wedged after played of playback.
func wedgedAt(played time.Duration) Outcome {
	fault := &health.Fault{Kind: health.Wedged, Subject: "the playing cast", Revise: true, Vitals: playedFor(played)}
	return Outcome{Err: fault, Evidence: Evidence{Reached: health.Playing, Verdict: health.Wedged, Vitals: fault.Vitals, EncodeErr: fault, Hardware: true}}
}

// playedFor is a playing reading at played.
func playedFor(played time.Duration) health.Vitals {
	return health.DeviceStarved("the playing cast", media.Progress{Bytes: 1 << 20, Position: played}, 1<<20).Vitals
}

func TestAHardwareEncodeThatFailedIsMadeAgainInSoftwareOnTheSameLink(t *testing.T) {
	for _, tt := range []struct {
		name   string
		failed Outcome
		from   time.Duration
	}{
		{"before playback, from the start", encodeFailedAt(0, true), 0},
		{"while playing, from where the viewer got to", encodeFailedAt(816*time.Second, true), 816 * time.Second},
		{"once it stopped making what it was fed", wedgedAt(816 * time.Second), 816 * time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			in := Intent{Turns: unheard{}, Candidates: candidates(t, "https://cdn.example/one.m3u8", "https://other.example/two.m3u8"), Deadline: 30 * time.Second}
			run := &scriptedRunner{outcomes: []Outcome{tt.failed, delivered}}
			if err := Cast(t.Context(), in, run, &fakeProgram{}); err != nil || len(run.seen) != 2 {
				t.Fatalf("Cast error = %v after %d attempts, want the second to deliver", err, len(run.seen))
			}
			if next := run.seen[1]; !next.Software || next.candidate != 0 || next.Program.Start != tt.from {
				t.Errorf("second attempt: software %t on candidate %d from %s, want software on the same link from %s", next.Software, next.candidate, next.Program.Start, tt.from)
			}
		})
	}
}

func TestAnEncodeThatFailedInSoftwareMovesToTheNextLinkWhereTheViewerWas(t *testing.T) {
	in := Intent{Turns: unheard{}, Candidates: candidates(t, "https://cdn.example/one.m3u8", "https://other.example/two.m3u8"), Deadline: 30 * time.Second}
	run := &scriptedRunner{outcomes: []Outcome{encodeFailedAt(600*time.Second, false), delivered}}
	if err := Cast(t.Context(), in, run, &fakeProgram{}); err != nil || len(run.seen) != 2 {
		t.Fatalf("Cast error = %v after %d attempts, want the second to deliver", err, len(run.seen))
	}
	if next := run.seen[1]; next.Software || next.candidate != 1 || next.Program.Start != 600*time.Second {
		t.Errorf("second attempt: software %t on candidate %d from %s, want the next link as it was from where the viewer was", next.Software, next.candidate, next.Program.Start)
	}
}

func TestASoftwareRetryThatFailsMovesToTheNextLinkWhichMayUseHardware(t *testing.T) {
	in := Intent{Turns: unheard{}, Candidates: candidates(t, "https://cdn.example/one.m3u8", "https://other.example/two.m3u8"), Deadline: 30 * time.Second}
	// The retry still reports hardware, so only Attempt.Software stops a second software retry.
	run := &scriptedRunner{outcomes: []Outcome{encodeFailedAt(0, true), encodeFailedAt(600*time.Second, true), delivered}}
	if err := Cast(t.Context(), in, run, &fakeProgram{}); err != nil || len(run.seen) != 3 {
		t.Fatalf("Cast error = %v after %d attempts, want the third to deliver", err, len(run.seen))
	}
	if third := run.seen[2]; third.Software || third.candidate != 1 || third.Program.Start != 600*time.Second {
		t.Errorf("third attempt: software %t on candidate %d from %s, want the next link, free to use hardware, from where the viewer was", third.Software, third.candidate, third.Program.Start)
	}
}

func TestAnEncodeThatFailedWhilePlayingWhereNobodyKnowsTheViewerEndsTheCast(t *testing.T) {
	in := Intent{Turns: unheard{}, Candidates: candidates(t, "https://cdn.example/one.m3u8", "https://other.example/two.m3u8"), Deadline: 30 * time.Second}
	unwatched := Outcome{Err: errExited, Evidence: Evidence{Reached: health.Playing, EncodeErr: errExited, Hardware: true}}
	run := &scriptedRunner{outcomes: []Outcome{unwatched, delivered}}
	if err := Cast(t.Context(), in, run, &fakeProgram{}); err == nil || len(run.seen) != 1 {
		t.Fatalf("Cast error = %v after %d attempts, want a refusal rather than a cast started over", err, len(run.seen))
	}
}

func TestAFailureThatIsNotTheEncodesIsNotAnsweredInSoftware(t *testing.T) {
	refusal := errors.New("SOAP 714")
	for _, tt := range []struct {
		name   string
		failed Outcome
	}{
		{"a device refusing the URL", Outcome{Err: refusal, Evidence: Evidence{Reached: health.Opening, PlayErr: refusal, Hardware: true}}},
		{"a source that gave out", func() Outcome { o := starvedAt(816 * time.Second); o.Evidence.Hardware = true; return o }()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			in := Intent{Turns: unheard{}, Candidates: candidates(t, "https://cdn.example/one.m3u8", "https://other.example/two.m3u8"), Deadline: 30 * time.Second}
			run := &scriptedRunner{outcomes: []Outcome{tt.failed, delivered, delivered}}
			_ = Cast(t.Context(), in, run, &fakeProgram{})
			if len(run.seen) < 2 || run.seen[1].Software {
				t.Fatalf("after %d attempts the next is in software, want it answered as before", len(run.seen))
			}
		})
	}
}

func TestARefusalQuotesTheEncoderAsItself(t *testing.T) {
	in := Intent{Turns: unheard{}, Candidates: candidates(t, "https://cdn.example/one.m3u8"), Deadline: 30 * time.Second}
	failed := encodeFailedAt(0, false)
	failed.Evidence.Lines = []string{"[fatal] Unknown encoder"}
	err := Cast(t.Context(), in, &scriptedRunner{outcomes: []Outcome{failed}}, &fakeProgram{})
	if err == nil || !strings.Contains(err.Error(), "the encoder said: [fatal] Unknown encoder") {
		t.Fatalf("refusal %v, want the encoder's own lines quoted as the encoder's", err)
	}
}
