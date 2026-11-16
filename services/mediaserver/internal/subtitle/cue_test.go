package subtitle

import (
	"strings"
	"sync"
	"testing"
)

// One row per signal that can end a cue.
func TestCueCut(t *testing.T) {
	for _, tt := range []struct {
		name  string
		words []Word
		want  int
	}{{
		name: "a sentence long enough to read closes on its punctuation",
		words: []Word{
			{Start: 0.0, End: 0.6, Text: "Ask"},
			{Start: 0.7, End: 1.3, Text: "not."}, // span 1.3s, at or over cueMinSeconds
			{Start: 1.4, End: 1.7, Text: "What"},
		},
		want: 2,
	}, {
		name: "a sentence too short to read stays open and coalesces",
		words: []Word{
			{Start: 0.0, End: 0.2, Text: "OK."},
			{Start: 0.3, End: 0.6, Text: "So"},
		},
		want: 0, // flashing it on its own is worse than merging it with what follows
	}, {
		name: "a silence closes the cue before it",
		words: []Word{
			{Start: 0.0, End: 0.3, Text: "before"},
			{Start: 2.0, End: 2.3, Text: "after"},
		},
		want: 1,
	}, {
		// The duration cap trips at the last word, but a pause is a better place to cut.
		name: "a cue over the duration cap falls back to the last pause",
		words: []Word{
			{Start: 0.0, End: 0.5, Text: "keep"},
			{Start: 0.6, End: 1.1, Text: "it"},
			{Start: 1.2, End: 2.0, Text: "simple"}, // 0.6s of silence follows
			{Start: 2.6, End: 3.5, Text: "and"},
			{Start: 3.6, End: 4.5, Text: "keep"},
			{Start: 4.6, End: 6.2, Text: "going"}, // span 6.2s, at or over cueMaxSeconds
		},
		want: 3,
	}, {
		name:  "an accented cue is budgeted in characters",
		words: accentedWords(10),
		want:  0,
	}} {
		t.Run(tt.name, func(t *testing.T) {
			if got := cueCut(tt.words); got != tt.want {
				t.Errorf("cueCut = %d, want %d", got, tt.want)
			}
		})
	}
}

func accentedWords(n int) []Word {
	words := make([]Word, n)
	for i := range words {
		start := float64(i) * 0.5
		words[i] = Word{Start: start, End: start + 0.3, Text: "\u00e9\u00e9\u00e9\u00e9\u00e9"}
	}
	return words
}

func TestCuesCloseInOrder(t *testing.T) {
	b := &Cues{}
	words := []Word{
		{Start: 0.0, End: 0.7, Text: "First"},
		{Start: 0.8, End: 1.4, Text: "line."}, // span 1.4s ≥ cueMinSeconds
		{Start: 3.0, End: 3.7, Text: "Second"},
		{Start: 3.8, End: 4.4, Text: "line."}, // span 1.4s ≥ cueMinSeconds
	}
	b.Commit(words, 5.0)

	cues := b.Snapshot(0, 100).Cues
	if len(cues) != 2 {
		t.Fatalf("want 2 cues, got %d: %v", len(cues), cues)
	}
	if cues[0].Start > cues[1].Start {
		t.Error("cues must be appended in non-decreasing start order")
	}
	if got := b.cueAt(3.2); got != "Second line." {
		t.Errorf("CueAt(3.2) = %q", got)
	}
	if got := b.cueAt(2.5); got != "" {
		t.Errorf("CueAt in the gap should be empty, got %q", got)
	}
}

func TestCuesSilentTailClosesTheFinalCue(t *testing.T) {
	b := &Cues{}
	sentence := []Word{
		{Start: 0.0, End: 0.7, Text: "The"},
		{Start: 0.8, End: 1.6, Text: "end"}, // no sentence punctuation
	}
	b.Commit(sentence, 1.6)
	if n := len(b.Snapshot(0, 100).Cues); n != 0 {
		t.Fatalf("cue without a closing signal should stay open, got %d cues", n)
	}
	b.Commit(nil, 1.6+cueGapSeconds)
	if n := len(b.Snapshot(0, 100).Cues); n != 1 {
		t.Fatalf("confirmed silence should close the trailing cue, got %d cues", n)
	}
}

func TestSnapshotWaitsForOpenCuesAndIncludesSilence(t *testing.T) {
	c := &Cues{}
	c.Commit([]Word{{Start: 2, End: 2.5, Text: "hello"}}, 2.5)
	if got := c.Snapshot(0, 10); got.SettledTo != 2 || len(got.Cues) != 0 || got.Complete {
		t.Fatalf("pending cue snapshot = %+v", got)
	}
	c.Commit(nil, 8)
	got := c.Snapshot(0, 10)
	if got.SettledTo != 8 || len(got.Cues) != 1 {
		t.Fatalf("silence snapshot = %+v", got)
	}
	got.Cues[0].Text = "mutated"
	if next := c.Snapshot(0, 10); next.Cues[0].Text != "hello" {
		t.Fatal("snapshot aliases committed cues")
	}
	c.Close()
	if !c.Snapshot(0, 10).Complete {
		t.Fatal("completion not published")
	}
}

func TestSnapshotIncludesOverlappingCuesAndPrunesConsumedCues(t *testing.T) {
	c := &Cues{}
	c.Commit([]Word{{Start: 2, End: 4, Text: "first."}, {Start: 7, End: 9, Text: "second."}}, 12)
	if got := c.Snapshot(3, 8); len(got.Cues) != 2 {
		t.Fatalf("overlap snapshot = %+v", got)
	}
	c.DiscardBefore(5)
	c.Commit(nil, 15)
	got := c.Snapshot(0, 20)
	if len(got.Cues) != 1 || got.Cues[0].Text != "second." || got.SettledTo != 15 {
		t.Fatalf("pruned snapshot = %+v", got)
	}
}

func TestLateHypothesesCannotRevisePublishedTimeline(t *testing.T) {
	c := &Cues{}
	c.Commit(nil, 6)
	c.Commit([]Word{{Start: 2, End: 4, Text: "too late"}, {Start: 5, End: 8, Text: "overlap."}}, 10)
	got := c.Snapshot(0, 12)
	if len(got.Cues) != 1 || got.Cues[0].Start < 6 || got.Cues[0].Text != "overlap." {
		t.Fatalf("late hypothesis changed settled timeline: %+v", got)
	}
}

func TestSnapshotsRemainSafeDuringPublicationAndPruning(t *testing.T) {
	c := &Cues{}
	var readers sync.WaitGroup
	readers.Go(func() {
		for range 1000 {
			snapshot := c.Snapshot(0, 3000)
			if len(snapshot.Cues) > 0 {
				snapshot.Cues[0].Text = "reader copy"
			}
		}
	})
	for i := range 1000 {
		start := float64(i * 3)
		c.DiscardBefore(start - 12)
		c.Commit([]Word{{Start: start, End: start + 2, Text: "caption."}}, start+3)
	}
	c.Close()
	readers.Wait()
	if got := c.Snapshot(0, 3000); len(got.Cues) > 5 {
		t.Fatalf("retained %d cues", len(got.Cues))
	}
}

func TestCompletedTranscriptReleasesConsumedCaptionsWithoutAnotherCommit(t *testing.T) {
	c := &Cues{}
	for i := range 100 {
		start := float64(i * 3)
		c.Commit([]Word{{Start: start, End: start + 2, Text: "caption."}}, start+3)
	}
	c.Close()
	c.DiscardBefore(294)
	snapshot := c.Snapshot(0, 400)
	if !snapshot.Complete || len(snapshot.Cues) != 2 || snapshot.SettledTo != 300 {
		t.Fatalf("completed prune = %+v", snapshot)
	}
	current := c.published.Load()
	if cap(current.Cues) > 4 {
		t.Fatalf("completed snapshot retains original backing array: cap=%d", cap(current.Cues))
	}
}

func TestConcurrentPruningDoesNotRestoreConsumedPrefix(t *testing.T) {
	c := &Cues{}
	var consumer sync.WaitGroup
	consumer.Go(func() {
		for i := range 1000 {
			c.DiscardBefore(float64(i*3 - 12))
		}
	})
	for i := range 1000 {
		start := float64(i * 3)
		c.Commit([]Word{{Start: start, End: start + 2, Text: "caption."}}, start+3)
	}
	c.Close()
	consumer.Wait()
	c.DiscardBefore(2994)
	if got := c.Snapshot(0, 4000); len(got.Cues) != 2 || !got.Complete {
		t.Fatalf("concurrent prune retained %d cues complete=%v", len(got.Cues), got.Complete)
	}
}

func wordsOf(widths ...int) []Word {
	words := make([]Word, len(widths))
	for i, n := range widths {
		start := float64(i) * 0.5
		words[i] = Word{Start: start, End: start + 0.4, Text: strings.Repeat("x", n)}
	}
	return words
}

func TestACueNeverNeedsMoreThanTwoLines(t *testing.T) {
	// 68 columns in all, under two lines' worth, but three 22-column words only wrap onto three lines.
	words := wordsOf(22, 22, 22)
	if fitsLines(words) {
		t.Fatal("three 22-column words fit on two 42-column lines")
	}
	if got := cueCut(words); got != 2 {
		t.Fatalf("cueCut = %d, want the cue cut before the word that needs a third line", got)
	}
	if !fitsLines(wordsOf(20, 20, 20, 20)) {
		t.Fatal("four 20-column words need only two lines")
	}
}

func TestCueTextBreaksAtTheBetterBoundary(t *testing.T) {
	for _, tt := range []struct {
		name string
		text string
		want string
	}{
		{"a short cue stays on one line", "Let's start with the basics.", "Let's start with the basics."},
		{"lines are balanced", "We have better maps of Mars than of our own seafloor", "We have better maps of Mars\nthan of our own seafloor"},
		{"a clause end is the better break", "At nine o'clock this morning, Mission Control lost contact", "At nine o'clock this morning,\nMission Control lost contact"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var words []Word
			for w := range strings.FieldsSeq(tt.text) {
				words = append(words, Word{Text: w})
			}
			if got := cueText(words); got != tt.want {
				t.Errorf("cueText = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestACueStaysUpLongEnoughToReadButNeverOverTheNextWord(t *testing.T) {
	words := []Word{{Start: 10, End: 10.6, Text: "I'm"}, {Start: 10.6, End: 11, Text: "sorry,"}, {Start: 11, End: 11.9, Text: "Dave, I can't do that."}}
	// 33 columns need 1.57s at 21 per second, more than the 1.55s the edge-trimmed words span.
	got := makeCue(words, 20)
	if want := float64(columns(got.Text)) / cueColumnsPerSecond; got.End-got.Start < want-1e-9 {
		t.Errorf("cue lasts %.2fs, want the %.2fs its text needs", got.End-got.Start, want)
	}
	if got := makeCue(words, 11.71); got.End != 11.71 {
		t.Errorf("cue ends at %.2f, want it held back by the next word at 11.71", got.End)
	}
	if got := makeCue(wordsOf(5), 99); got.End != 0.4 {
		t.Errorf("an easily read cue now ends at %.2f, want its words' own 0.4", got.End)
	}
}
