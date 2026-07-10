package executor

import (
	"fmt"
	"testing"
)

func TestEvalLike(t *testing.T) {
	cases := []struct {
		text, pattern string
		want          bool
	}{
		{"hello", "hello", true},
		{"hello", "world", false},
		{"hello", "%", true},
		{"", "%", true},
		{"hello", "h%", true},
		{"hello", "%o", true},
		{"hello", "%ell%", true},
		{"hello", "%xyz%", false},
		{"hello", "h%o", true},
		{"hello", "h%x", false},
		{"a", "a%a", false},
		{"aa", "a%a", true},
		{"abcdef", "a%c%e%", true},
		{"abcdef", "a%e%c%", false},
		// '_' — single character (regexp path)
		{"hello", "h_llo", true},
		{"hello", "h_y", false},
		{"hello", "_____", true},
		{"hello", "____", false},
		// Regex special chars in pattern are escaped
		{"a.b", "a.b", true},
		{"axb", "a.b", false},
		{"price (usd)", "%(usd)%", true},
		// Multi-line text: % covers \n
		{"line1\nline2", "line1%line2", true},
	}

	for _, c := range cases {
		got, err := evalLike(c.text, c.pattern)
		if err != nil {
			t.Fatalf("evalLike(%q, %q): %v", c.text, c.pattern, err)
		}
		if got != c.want {
			t.Errorf("evalLike(%q, %q) = %v, want %v", c.text, c.pattern, got, c.want)
		}
	}
}

func TestEvalLikeNulls(t *testing.T) {
	if got, _ := evalLike(nil, "%"); got {
		t.Error("NULL LIKE '%' must be false")
	}
	if got, _ := evalLike("x", nil); got {
		t.Error("'x' LIKE NULL must be false")
	}
}

func TestEvalILike(t *testing.T) {
	cases := []struct {
		text, pattern string
		want          bool
	}{
		{"Hello", "hello", true},
		{"HELLO", "%ello", true},
		{"HeLLo", "h%o", true},
		{"hello", "WORLD", false},
		{"", "%", true},
		{"Hello World", "%world", true},
		{"Hello World", "%WORLD", true},
		{"abc", "A%C", true},
		{"ABC", "%b%", true},
		{"Hello", "h_llo", true},
		{"HELLO", "H_LLO", true},
		{"HELLO", "h_llo", true},
		{"hello", "HELLO", true},
		{"Hello", "hello", true},
		{"hElLo", "%El%", true},
	}

	for _, c := range cases {
		got, err := evalILike(c.text, c.pattern)
		if err != nil {
			t.Fatalf("evalILike(%q, %q): %v", c.text, c.pattern, err)
		}
		if got != c.want {
			t.Errorf("evalILike(%q, %q) = %v, want %v", c.text, c.pattern, got, c.want)
		}
	}
}

func TestEvalILikeNulls(t *testing.T) {
	if got, _ := evalILike(nil, "%"); got {
		t.Error("NULL ILIKE '%' must be false")
	}
	if got, _ := evalILike("x", nil); got {
		t.Error("'x' ILIKE NULL must be false")
	}
}

func TestLikeCacheEviction(t *testing.T) {
	c := newLikePatternCache(2)
	for i := 0; i < 5; i++ {
		if _, err := c.getOrCompile(fmt.Sprintf("pat%d%%", i)); err != nil {
			t.Fatal(err)
		}
	}
	if c.order.Len() != 2 || len(c.entries) != 2 {
		t.Fatalf("cache size = %d/%d, want 2", c.order.Len(), len(c.entries))
	}
}

func BenchmarkEvalLikeSimple(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = evalLike("some moderately long string value", "%long%")
	}
}

func BenchmarkEvalLikeComplex(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = evalLike("some moderately long string value", "s_me%v_lue")
	}
}
