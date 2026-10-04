package follow

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

// playlist is what the ledger renders, one line per URI or tag.
func playlist(l *ledger) []string {
	return strings.Split(strings.TrimSpace(string(l.Render(asListed))), "\n")
}

func count(lines []string, prefix string) int {
	n := 0
	for _, line := range lines {
		if strings.HasPrefix(line, prefix) {
			n++
		}
	}
	return n
}

func TestAnEncoderThatNumbersFromZeroAgainKeepsTheTimelineMovingForward(t *testing.T) {
	var l ledger
	l.Merge(listed("a", 0, 9))
	restarted := listed("b", 0, 2)
	l.Merge(restarted)
	lines := playlist(&l)
	if got := count(lines, "#EXT-X-MEDIA-SEQUENCE:3"); got != 1 {
		t.Errorf("the sequence did not move forward past the restart:\n%s", l.Render(asListed))
	}
	if got := count(lines, "#EXT-X-DISCONTINUITY"); got != 1 {
		t.Errorf("marked %d seams, want the one restart:\n%s", got, l.Render(asListed))
	}
	if !strings.Contains(string(l.Render(asListed)), "#EXT-X-DISCONTINUITY\n#EXTINF:1.000000,\nhttps://origin.example/b000.ts") {
		t.Errorf("the seam is not in front of the restarted encoder's first segment:\n%s", l.Render(asListed))
	}
}

func TestAStaleEdgeServingAnOlderWindowAddsNothing(t *testing.T) {
	var l ledger
	l.Merge(listed("a", 0, 9))
	l.Merge(listed("a", 4, 7))
	if got := count(playlist(&l), "#EXT-X-DISCONTINUITY"); got != 0 {
		t.Errorf("a stale window opened %d seams", got)
	}
}

// A CDN edge can lag further than castor retains, and what it lists was still published here.
func TestAStaleWindowOlderThanWhatIsRetainedAddsNothing(t *testing.T) {
	var l ledger
	for first := int64(0); first <= 7; first++ {
		l.Merge(listed("a", first, first+2))
	}
	before := string(l.Render(asListed))
	l.Merge(listed("a", 2, 4))
	if string(l.Render(asListed)) != before {
		t.Errorf("a window three reloads stale rendered:\n%s\nwant nothing added to:\n%s", l.Render(asListed), before)
	}
}

func TestSegmentsTheOriginSkippedAreASeamNotASilence(t *testing.T) {
	var l ledger
	l.Merge(listed("a", 0, 3))
	l.Merge(listed("a", 7, 9))
	if got := count(playlist(&l), "#EXT-X-DISCONTINUITY"); got != 1 {
		t.Errorf("marked %d seams at the loss, want 1", got)
	}
}

func TestASlidingWindowOnlyAppendsWhatIsNew(t *testing.T) {
	var l ledger
	for first := int64(0); first < 20; first += 2 {
		l.Merge(listed("a", first, first+5))
	}
	lines := playlist(&l)
	if got := count(lines, "#EXT-X-DISCONTINUITY"); got != 0 {
		t.Errorf("a window sliding forward opened %d seams", got)
	}
	if lines[len(lines)-1] != "https://origin.example/a023.ts" {
		t.Errorf("the playlist ends at %q, want the origin's edge", lines[len(lines)-1])
	}
}

func TestTheLedgerKeepsAsMuchAsTheOriginsLongestWindow(t *testing.T) {
	var l ledger
	l.Merge(listed("a", 0, 9))
	l.Merge(listed("b", 0, 0))
	for n := int64(1); n <= 12; n++ {
		l.Merge(listed("b", n, n))
	}
	lines := playlist(&l)
	if got := count(lines, "#EXTINF"); got != 10 {
		t.Errorf("kept %d segments, want the origin's longest window of 10", got)
	}
	if !strings.Contains(string(l.Render(asListed)), "#EXT-X-DISCONTINUITY-SEQUENCE:1\n") {
		t.Errorf("the trimmed seam is not counted in EXT-X-DISCONTINUITY-SEQUENCE:\n%s", l.Render(asListed))
	}
	if !strings.Contains(string(l.Render(asListed)), "#EXT-X-MEDIA-SEQUENCE:13\n") {
		t.Errorf("the sequence did not move forward with the trim:\n%s", l.Render(asListed))
	}
}

func TestASeamKeepsItsDiscontinuityNumberOnceItsTagScrollsOff(t *testing.T) {
	var l ledger
	l.Merge(listed("a", 0, 2))
	for n := int64(0); n <= 2; n++ {
		l.Merge(listed("b", n, n))
	}
	// b000 now opens the playlist, so the tag in front of it is gone and the sequence must count it.
	lines := playlist(&l)
	if slices.Contains(lines, "#EXT-X-DISCONTINUITY") || count(lines, "#EXT-X-DISCONTINUITY-SEQUENCE:1") != 1 {
		t.Errorf("the seam whose tag scrolled off is not counted, so its segments' discontinuity number went back:\n%s", l.Render(asListed))
	}
}

func TestAStartMeasuredFromTheOriginsFirstSegmentIsMovedOntoTheLedgers(t *testing.T) {
	var l ledger
	l.Merge(listed("a", 0, 9))
	w := listed("a", 3, 8)
	w.Start = &timeline.Start{Offset: 2 * time.Second, Precise: true}
	l.Merge(w)
	if !strings.Contains(string(l.Render(asListed)), "#EXT-X-START:TIME-OFFSET=5.000000,PRECISE=YES\n") {
		t.Errorf("the start was not rebased by the three segments the ledger holds before the origin's first:\n%s", l.Render(asListed))
	}
	w.Start = &timeline.Start{Offset: 0}
	l.Merge(w)
	if !strings.Contains(string(l.Render(asListed)), "#EXT-X-START:TIME-OFFSET=3.000000\n") {
		t.Errorf("a start at the origin's first segment was lost:\n%s", l.Render(asListed))
	}
}

func TestAClosedOriginEndsThePlaylistAndMergesNoMore(t *testing.T) {
	var l ledger
	w := listed("a", 0, 3)
	w.Closed = true
	l.Merge(w)
	before := string(l.Render(asListed))
	l.Merge(listed("a", 4, 6))
	if after := string(l.Render(asListed)); after != before {
		t.Errorf("merged segments after the origin closed:\n%s", after)
	}
	if lines := playlist(&l); lines[len(lines)-1] != "#EXT-X-ENDLIST" {
		t.Errorf("a closed origin's playlist does not end:\n%s", l.Render(asListed))
	}
}

func TestKeysAndInitSectionsAreWrittenWhereTheyChange(t *testing.T) {
	var l ledger
	w := listed("a", 0, 3)
	for i := range w.Segments {
		w.Segments[i].Map = &timeline.Map{URI: "https://origin.example/init.mp4", Range: timeline.Range{Offset: 0, Length: 720}}
		w.Segments[i].Range = timeline.Range{Offset: int64(720 + i*1000), Length: 1000}
		w.Segments[i].Key = timeline.Key{Method: "AES-128", URI: "https://origin.example/k", IV: fmt.Sprintf("0x%032x", i)}
	}
	w.Segments[3].Key = timeline.Key{}
	l.Merge(w)
	lines := playlist(&l)
	if got := count(lines, "#EXT-X-MAP"); got != 1 {
		t.Errorf("wrote %d init sections for one unchanged init", got)
	}
	if got := count(lines, "#EXT-X-KEY:METHOD=AES-128"); got != 3 {
		t.Errorf("wrote %d keys, want one per distinct IV", got)
	}
	for _, want := range []string{
		`#EXT-X-MAP:URI="https://origin.example/init.mp4",BYTERANGE="720@0"`,
		"#EXT-X-BYTERANGE:1000@1720",
		`#EXT-X-KEY:METHOD=AES-128,URI="https://origin.example/k",IV=0x00000000000000000000000000000002`,
		"#EXT-X-KEY:METHOD=NONE",
	} {
		if count(lines, want) != 1 {
			t.Errorf("missing %q in:\n%s", want, l.Render(asListed))
		}
	}
}

func TestPeriodsAreSeamsAndOnesAlreadyPlayedAreNotReplayed(t *testing.T) {
	period := func(name string, from, to int64) []timeline.Segment {
		w := listed(name, from, to)
		for i := range w.Segments {
			w.Segments[i].Place.Period = name
		}
		return w.Segments
	}
	var l ledger
	l.Merge(timeline.Window{Segments: append(period("film", 0, 3), period("ad", 0, 1)...)})
	// A live MPD that dropped the film Period and lists the ad and what follows.
	l.Merge(timeline.Window{Segments: append(period("ad", 0, 1), period("film2", 0, 2)...)})
	lines := playlist(&l)
	if got := count(lines, "https://origin.example/ad000.ts"); got != 1 {
		t.Errorf("the ad Period was published %d times, want once:\n%s", got, l.Render(asListed))
	}
	if lines[len(lines)-1] != "https://origin.example/film2002.ts" {
		t.Errorf("the playlist does not end at the next Period's edge:\n%s", l.Render(asListed))
	}
	if got := count(lines, "#EXT-X-DISCONTINUITY"); got != 2 {
		t.Errorf("marked %d seams, want one per Period boundary", got)
	}
}

func TestAStaleWindowFromAPeriodAlreadyPlayedIsNotReplayed(t *testing.T) {
	var l ledger
	film, ad := listed("film", 0, 1), listed("ad", 0, 1)
	for i := range 2 {
		film.Segments[i].Place.Period, ad.Segments[i].Place.Period = "film", "ad"
	}
	l.Merge(film)
	l.Merge(ad)
	// An edge still listing the film after castor trimmed it.
	before := string(l.Render(asListed))
	l.Merge(timeline.Window{Segments: film.Segments[1:]})
	if after := string(l.Render(asListed)); after != before {
		t.Errorf("replayed segments of a Period already played:\n%s", after)
	}
}

func TestTheTargetDurationNeverShrinks(t *testing.T) {
	var l ledger
	long := listed("a", 0, 0)
	long.Segments[0].Duration = 6 * time.Second
	l.Merge(long)
	for n := int64(1); n <= 3; n++ {
		l.Merge(listed("a", n, n))
	}
	if !strings.Contains(string(l.Render(asListed)), "#EXT-X-TARGETDURATION:6\n") {
		t.Errorf("the target shrank once the long segment was trimmed:\n%s", l.Render(asListed))
	}
}

// asListed renders every segment as the origin listed it.
func asListed(s timeline.Segment, _ int64) timeline.Segment { return s }
