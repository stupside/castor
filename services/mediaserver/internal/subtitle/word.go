package subtitle

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Piece is one text token a recognizer emitted, with absolute times in seconds.
type Piece struct {
	Start, End float64
	Text       string // as the model wrote it, a leading space starting a word
}

// GroupPieces glues sub-word and partial-byte tokens into words. A leading space starts a
// word, and so does each character of a script written without spaces.
func GroupPieces(pieces []Piece) []Word {
	var words []Word
	for _, p := range pieces {
		text := strings.TrimLeft(p.Text, " ")
		if text == "" {
			continue
		}
		if last := len(words) - 1; last >= 0 && len(text) == len(p.Text) && !startsUnspacedChar(text, words[last].Text) {
			words[last].Text += text
			words[last].End = max(words[last].End, p.End)
			continue
		}
		words = append(words, Word{Start: p.Start, End: p.End, Text: text})
	}
	return words
}

// startsUnspacedChar reports whether text opens a new character of a script without spaces, after a complete word.
func startsUnspacedChar(text, previous string) bool {
	r, _ := utf8.DecodeRuneInString(text)
	return unspaced(r) && utf8.ValidString(previous)
}

// unspaced reports whether r belongs to a script whose words are not separated by spaces.
func unspaced(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Thai, unicode.Lao, unicode.Khmer, unicode.Myanmar)
}

// spaceBetween reports whether two neighbouring words are written with a space between them,
// which a script without spaces never has, even next to a digit or a latin word.
func spaceBetween(left, right string) bool {
	l, _ := utf8.DecodeLastRuneInString(left)
	r, _ := utf8.DecodeRuneInString(right)
	return !unspaced(l) && !unspaced(r)
}

// columns is the display width of s, where East Asian wide characters take two.
func columns(s string) int {
	n := 0
	for _, r := range s {
		n++
		if wide(r) {
			n++
		}
	}
	return n
}

func wide(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) ||
		r >= 0x3000 && r <= 0x303F || r >= 0xFF00 && r <= 0xFF60 || r >= 0xFFE0 && r <= 0xFFE6
}
