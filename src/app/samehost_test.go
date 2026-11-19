package app

import "testing"

func TestSameHostSendsTheTokenOnlyWhereItIsSafe(t *testing.T) {
	for _, c := range []struct {
		base, link string
		want       bool
	}{
		{"https://x.example", "https://x.example/d/a", true},
		{"https://X.example", "https://x.EXAMPLE/d/a", true},
		{"http://x.example:5244", "http://x.example:5244/d/a", true},
		{"http://x.example", "https://x.example/d/a", true},  // up to https is safe
		{"https://x.example", "http://x.example/d/a", false}, // no downgrade to the clear
		{"https://x.example", "https://cdn.example/d/a", false},
		{"https://x.example:5244", "https://x.example/d/a", false},
		{"", "/d/a", false},
		{"https://x.example", "/d/a", false},
		{"", "", false},
	} {
		if got := sameHost(c.base, c.link); got != c.want {
			t.Errorf("sameHost(%q, %q) = %v, want %v", c.base, c.link, got, c.want)
		}
	}
}
