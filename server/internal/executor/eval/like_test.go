package eval_test

import (
	"testing"

	"vaultdb/internal/executor/eval"
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
		{"hello", "h_llo", true},
		{"hello", "h_y", false},
		{"hello", "_____", true},
		{"hello", "____", false},
		{"a.b", "a.b", true},
		{"axb", "a.b", false},
		{"price (usd)", "%(usd)%", true},
		{"line1\nline2", "line1%line2", true},
	}

	for _, c := range cases {
		got, err := eval.EvalLike(c.text, c.pattern)
		if err != nil {
			t.Fatalf("EvalLike(%q, %q): %v", c.text, c.pattern, err)
		}
		if got != c.want {
			t.Errorf("EvalLike(%q, %q) = %v, want %v", c.text, c.pattern, got, c.want)
		}
	}
}

func TestEvalLikeNulls(t *testing.T) {
	if got, _ := eval.EvalLike(nil, "%"); got {
		t.Error("NULL LIKE '%' must be false")
	}
	if got, _ := eval.EvalLike("x", nil); got {
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
		got, err := eval.EvalILike(c.text, c.pattern)
		if err != nil {
			t.Fatalf("EvalILike(%q, %q): %v", c.text, c.pattern, err)
		}
		if got != c.want {
			t.Errorf("EvalILike(%q, %q) = %v, want %v", c.text, c.pattern, got, c.want)
		}
	}
}

func TestEvalILikeNulls(t *testing.T) {
	if got, _ := eval.EvalILike(nil, "%"); got {
		t.Error("NULL ILIKE '%' must be false")
	}
	if got, _ := eval.EvalILike("x", nil); got {
		t.Error("'x' ILIKE NULL must be false")
	}
}

func BenchmarkEvalLikeSimple(b *testing.B) {
	for i := 0; i < b.N; i++ {
		eval.EvalLike("hello world", "hello%")
	}
}

func BenchmarkEvalILikeSimple(b *testing.B) {
	for i := 0; i < b.N; i++ {
		eval.EvalILike("hello world", "HELLO%")
	}
}
