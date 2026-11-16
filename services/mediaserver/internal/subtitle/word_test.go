package subtitle

import (
	"slices"
	"testing"
	"unicode/utf8"
)

func TestGroupPiecesGluesSubwordsAndSplitsUnspacedScripts(t *testing.T) {
	for _, tt := range []struct {
		name   string
		pieces []string
		want   []string
	}{
		{"a leading space starts a word, other tokens extend it", []string{" Amer", "icans", ",", " ask"}, []string{"Americans,", "ask"}},
		{"each character of a script without spaces is its own word", []string{"各位", "晚上", "好", ","}, []string{"各位", "晚上", "好,"}},
		{"latin after a script starts its own word", []string{"已经", " 20", "年"}, []string{"已经", "20", "年"}},
		{"a token that is only a space is nothing", []string{" ", " so"}, []string{"so"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var pieces []Piece
			for i, text := range tt.pieces {
				pieces = append(pieces, Piece{Start: float64(i), End: float64(i) + 1, Text: text})
			}
			var got []string
			for _, w := range GroupPieces(pieces) {
				got = append(got, w.Text)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("GroupPieces = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGroupPiecesSpansTheGluedTokens(t *testing.T) {
	got := GroupPieces([]Piece{{Start: 1, End: 1.2, Text: " Amer"}, {Start: 1.2, End: 1.9, Text: "icans"}})
	if len(got) != 1 || got[0].Start != 1 || got[0].End != 1.9 {
		t.Fatalf("GroupPieces = %+v, want one word over [1, 1.9]", got)
	}
}

func TestCuesAreJoinedByTheirScript(t *testing.T) {
	for _, tt := range []struct {
		words []string
		want  string
	}{
		{[]string{"Hello", "world."}, "Hello world."},
		{[]string{"各位", "晚上", "好,"}, "各位晚上好,"},
		{[]string{"已经", "20", "年"}, "已经20年"},
		{[]string{"We", "met", "在", "Paris."}, "We met在Paris."},
	} {
		var words []Word
		for _, text := range tt.words {
			words = append(words, Word{Text: text})
		}
		if got := joinWords(words); got != tt.want {
			t.Errorf("joinWords(%q) = %q, want %q", tt.words, got, tt.want)
		}
	}
}

func TestColumnsCountWideCharactersTwice(t *testing.T) {
	if got := columns("abc"); got != 3 {
		t.Errorf("columns(abc) = %d", got)
	}
	if got := columns("été"); got != 3 {
		t.Errorf("columns(été) = %d, want columns not bytes", got)
	}
	if got := columns("各位。"); got != 6 {
		t.Errorf("columns(各位。) = %d, want 6", got)
	}
}

func TestGroupPiecesNeverEndsAWordInsideACharacter(t *testing.T) {
	// 欢 is three bytes, here split over two tokens.
	words := GroupPieces([]Piece{{Text: "好"}, {Text: "\xe6\xac"}, {Text: "\xa2"}, {Text: "迎"}})
	var joined string
	for _, w := range words {
		if !utf8.ValidString(w.Text) {
			t.Errorf("word %q is not valid UTF-8", w.Text)
		}
		joined += w.Text
	}
	if joined != "好欢迎" {
		t.Errorf("words join to %q", joined)
	}
}
