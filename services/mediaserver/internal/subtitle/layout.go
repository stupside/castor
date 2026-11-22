package subtitle

import "strings"

const (
	// Broadcast subtitle limits, the ones the IWSLT subtitling track scores against.
	cueLineColumns       = 42
	cueMaxLines          = 2
	cueColumnsPerSecond  = 21
	lineBreakWeakPenalty = 15
)

// fitsLines reports whether the words wrap into at most cueMaxLines lines of cueLineColumns.
func fitsLines(words []Word) bool {
	lines, used := 1, 0
	for i, w := range words {
		n := columns(w.Text)
		switch {
		case i == 0:
			used = n
		case used+spaceWidth(words[i-1].Text, w.Text)+n <= cueLineColumns:
			used += spaceWidth(words[i-1].Text, w.Text) + n
		default:
			lines++
			used = n
		}
	}
	return lines <= cueMaxLines
}

func spaceWidth(left, right string) int {
	if spaceBetween(left, right) {
		return 1
	}
	return 0
}

func joinWords(words []Word) string {
	var b strings.Builder
	for i, w := range words {
		if i > 0 && spaceBetween(words[i-1].Text, w.Text) {
			b.WriteByte(' ')
		}
		b.WriteString(w.Text)
	}
	return b.String()
}

// cueText lays words out on one line, or two balanced lines broken at the best-scoring word boundary.
func cueText(words []Word) string {
	flat := joinWords(words)
	if columns(flat) <= cueLineColumns || len(words) < 2 {
		return flat
	}
	best, bestScore := 1, breakScore(words, 1)
	for k := 2; k < len(words); k++ {
		if score := breakScore(words, k); score < bestScore {
			best, bestScore = k, score
		}
	}
	return joinWords(words[:best]) + "\n" + joinWords(words[best:])
}

// breakScore is lower for a better break: no line over the limit first, then lines of even length ending a clause.
func breakScore(words []Word, at int) int {
	first, second := joinWords(words[:at]), joinWords(words[at:])
	a, b := columns(first), columns(second)
	mean := (a + b) / 2
	score := 100*(max(0, a-cueLineColumns)+max(0, b-cueLineColumns)) + max(a-mean, mean-a) + max(b-mean, mean-b)
	if !clauseEnd(first) {
		score += lineBreakWeakPenalty
	}
	return score
}
