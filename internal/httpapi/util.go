package httpapi

import (
	"strconv"
	"strings"
)

func parseInt64(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func parseSlug(s string) (string, bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || len(s) > 32 {
		return "", false
	}
	if s[0] == '-' || s[len(s)-1] == '-' {
		return "", false
	}
	prevHyphen := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '-' {
			if prevHyphen {
				return "", false
			}
			prevHyphen = true
			continue
		}
		prevHyphen = false
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			continue
		}
		return "", false
	}
	return s, true
}

