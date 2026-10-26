package cast

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseStart reads a position as a duration (1h12m) or a clock (1:12 is H:MM, 1:12:30 is H:MM:SS); empty is the beginning.
func ParseStart(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	bad := fmt.Errorf("%q is not a position such as 1h12m or 1:12", s)
	if !strings.Contains(s, ":") {
		d, err := time.ParseDuration(s)
		if err != nil || d < 0 {
			return 0, bad
		}
		return d, nil
	}
	parts := strings.Split(s, ":")
	if len(parts) > 3 {
		return 0, bad
	}
	var d time.Duration
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || i > 0 && n > 59 {
			return 0, bad
		}
		d = d*60 + time.Duration(n)
	}
	if len(parts) == 2 {
		return d * time.Minute, nil
	}
	return d * time.Second, nil
}
