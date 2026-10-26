package source

import (
	"strings"

	"golang.org/x/text/language"
)

// LanguageRank is how early language comes in languages, higher for earlier, zero where it is none of them.
func LanguageRank(language string, languages []string) int {
	for i, preferred := range languages {
		if SameLanguage(language, preferred) {
			return len(languages) - i
		}
	}
	return 0
}

// SameLanguage compares two language codes on their primary subtag, so en matches en-US; an empty code matches nothing.
func SameLanguage(a, b string) bool {
	primary := func(s string) string {
		head, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(s)), "-")
		if head == "" {
			return ""
		}
		if tag, err := language.Parse(head); err == nil {
			base, _ := tag.Base()
			return base.String()
		}
		return head
	}
	return primary(a) != "" && primary(a) == primary(b)
}
