package cli

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// parseSelection turns "1,2,5-7" / "all" / "*" into sorted unique 1-based
// indices in [1, max]. Errors on empty, malformed, or out-of-range input.
func parseSelection(input string, max int) ([]int, error) {
	s := strings.TrimSpace(strings.ToLower(input))
	if s == "" {
		return nil, fmt.Errorf("empty selection")
	}
	if s == "all" || s == "*" {
		out := make([]int, max)
		for i := range out {
			out[i] = i + 1
		}
		return out, nil
	}
	set := map[int]bool{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if lo, hi, ok := strings.Cut(part, "-"); ok {
			a, err1 := strconv.Atoi(strings.TrimSpace(lo))
			b, err2 := strconv.Atoi(strings.TrimSpace(hi))
			if err1 != nil || err2 != nil || a > b {
				return nil, fmt.Errorf("bad range %q", part)
			}
			for i := a; i <= b; i++ {
				if i < 1 || i > max {
					return nil, fmt.Errorf("index %d out of range 1..%d", i, max)
				}
				set[i] = true
			}
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("bad index %q", part)
		}
		if n < 1 || n > max {
			return nil, fmt.Errorf("index %d out of range 1..%d", n, max)
		}
		set[n] = true
	}
	if len(set) == 0 {
		return nil, fmt.Errorf("empty selection")
	}
	out := make([]int, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Ints(out)
	return out, nil
}
