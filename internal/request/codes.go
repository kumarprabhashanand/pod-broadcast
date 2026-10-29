package request

import (
	"fmt"
	"strconv"
	"strings"
)

// CodeSet matches HTTP status codes by class ("2xx") or exactly ("404").
type CodeSet struct {
	classes [6]bool
	codes   map[int]bool
	text    string
}

// ParseCodeSet parses a comma-separated list such as "2xx,404".
func ParseCodeSet(s string) (CodeSet, error) {
	set := CodeSet{codes: map[int]bool{}, text: s}
	for _, item := range strings.Split(s, ",") {
		item = strings.ToLower(strings.TrimSpace(item))
		if len(item) == 3 && strings.HasSuffix(item, "xx") && item[0] >= '1' && item[0] <= '5' {
			set.classes[item[0]-'0'] = true
			continue
		}
		code, err := strconv.Atoi(item)
		if err != nil || code < 100 || code > 599 {
			return CodeSet{}, fmt.Errorf("invalid status code %q in %q: use classes like 2xx or codes like 404", item, s)
		}
		set.codes[code] = true
	}
	return set, nil
}

func (c CodeSet) Match(code int) bool {
	return c.codes[code] || (code >= 100 && code <= 599 && c.classes[code/100])
}

func (c CodeSet) String() string { return c.text }
