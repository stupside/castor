package strategy

import (
	"fmt"
	"strconv"
	"strings"
)

// Rate reads a setting in bits a second, or bits, with an optional k or M such as 3M.
func Rate(text string) (int, error) {
	digits, unit := strings.TrimSpace(text), 1
	switch {
	case strings.HasSuffix(digits, "k"):
		digits, unit = strings.TrimSuffix(digits, "k"), 1_000
	case strings.HasSuffix(digits, "M"):
		digits, unit = strings.TrimSuffix(digits, "M"), 1_000_000
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n <= 0 || n > (1<<62)/unit {
		return 0, fmt.Errorf("want an amount such as 3M, not %q", text)
	}
	return n * unit, nil
}
