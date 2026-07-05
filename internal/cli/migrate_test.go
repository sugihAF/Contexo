package cli

import (
	"reflect"
	"testing"
)

func TestParseSelection(t *testing.T) {
	ok := []struct {
		in   string
		max  int
		want []int
	}{
		{"all", 3, []int{1, 2, 3}},
		{"*", 2, []int{1, 2}},
		{"1,2,5-7", 10, []int{1, 2, 5, 6, 7}},
		{" 3 , 1 ", 3, []int{1, 3}},
		{"2-2", 5, []int{2}},
	}
	for _, c := range ok {
		got, err := parseSelection(c.in, c.max)
		if err != nil {
			t.Errorf("parseSelection(%q): unexpected err %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseSelection(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	bad := []struct {
		in  string
		max int
	}{
		{"", 3}, {"0", 3}, {"4", 3}, {"5-3", 10}, {"a", 3}, {"1-", 3},
	}
	for _, c := range bad {
		if _, err := parseSelection(c.in, c.max); err == nil {
			t.Errorf("parseSelection(%q, max=%d) expected error", c.in, c.max)
		}
	}
}
