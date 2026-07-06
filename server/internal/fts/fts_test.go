package fts

import (
	"testing"
)

// --- Tokenizer tests ---

func TestTokenize(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{input: "Hello, World!", want: []string{"hello", "world"}},
		{input: "it's a test", want: []string{"it", "s", "a", "test"}},
		{input: "BM25-scoring_v2", want: []string{"bm25", "scoring", "v2"}},
		{input: "", want: nil},
		{input: "UPPER CASE", want: []string{"upper", "case"}},
		{input: "123 456", want: []string{"123", "456"}},
		{input: "hello.world", want: []string{"hello", "world"}},
	}
	for _, tt := range tests {
		got := Tokenize(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("Tokenize(%q) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("Tokenize(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

func TestTokenizeNoStop(t *testing.T) {
	got := TokenizeNoStop("the quick brown fox")
	want := []string{"quick", "brown", "fox"}
	if len(got) != len(want) {
		t.Fatalf("TokenizeNoStop = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("TokenizeNoStop[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestTokenizeNoStopRemovesAllStopWords(t *testing.T) {
	got := TokenizeNoStop("a an is are was were be been being")
	if len(got) != 0 {
		t.Errorf("TokenizeNoStop all-stopwords = %v, want empty", got)
	}
}

// --- BM25 tests ---

func TestBM25_SingleTerm(t *testing.T) {
	b := NewBM25()
	doc := []string{"quick", "brown", "fox"}
	query := []string{"quick"}
	df := map[string]int{"quick": 1}
	score := b.Score(doc, query, 3.0, df, 1)
	if score <= 0 {
		t.Errorf("BM25 single term score = %f, want > 0", score)
	}
}

func TestBM25_NoMatch(t *testing.T) {
	b := NewBM25()
	doc := []string{"quick", "brown", "fox"}
	query := []string{"zzzzz"}
	df := map[string]int{"zzzzz": 1}
	score := b.Score(doc, query, 3.0, df, 1)
	if score != 0 {
		t.Errorf("BM25 no match score = %f, want 0", score)
	}
}

func TestBM25_MultipleTerms(t *testing.T) {
	b := NewBM25()
	doc1 := []string{"quick", "brown", "fox"}
	doc2 := []string{"quick", "lazy", "dog"}
	query := []string{"quick", "fox"}
	df := map[string]int{"quick": 2, "fox": 1, "brown": 1, "lazy": 1, "dog": 1}
	s1 := b.Score(doc1, query, 3.0, df, 2)
	s2 := b.Score(doc2, query, 3.0, df, 2)
	if s1 <= s2 {
		t.Errorf("BM25: doc1 with both terms (%f) should score higher than doc2 with one term (%f)", s1, s2)
	}
}

func TestBM25_RarerTermHigherIDF(t *testing.T) {
	b := NewBM25()
	doc := []string{"the", "cat"}
	query := []string{"cat"}
	df := map[string]int{"the": 100, "cat": 1}
	smallDf := b.Score(doc, query, 2.0, df, 100)

	df2 := map[string]int{"the": 1, "cat": 100}
	largeDf := b.Score(doc, query, 2.0, df2, 100)

	if smallDf <= largeDf {
		t.Errorf("BM25 IDF: rare term score %f should be > common term score %f", smallDf, largeDf)
	}
}

// --- Corpus tests ---

func TestCorpus_Score(t *testing.T) {
	c := NewCorpus()
	c.IndexDoc(Tokenize("the quick brown fox"))
	c.IndexDoc(Tokenize("a quick lazy dog"))
	c.IndexDoc(Tokenize("the fox is quick and brown"))

	query := Tokenize("quick fox")
	docs := [][]string{
		Tokenize("the quick brown fox"),
		Tokenize("a quick lazy dog"),
		Tokenize("the fox is quick and brown"),
	}
	scores := make([]float64, len(docs))
	for i, doc := range docs {
		scores[i] = c.ScoreDocument(doc, query)
	}
	// Doc 1 ("quick brown fox") should score higher than doc 2 ("quick lazy dog")
	if scores[1] >= scores[0] {
		t.Errorf("doc1 (%f) should score higher than doc2 (%f)", scores[0], scores[1])
	}
	// Doc 3 ("fox quick brown") should also score higher than doc 2
	if scores[1] >= scores[2] {
		t.Errorf("doc1 (%f) should score higher than doc3 (%f)", scores[0], scores[2])
	}
}

// --- Snippet tests ---

func TestHighlight(t *testing.T) {
	text := "The quick brown fox jumps over the lazy dog"
	got := Highlight(text, []string{"brown"}, 30)
	if len(got) == 0 {
		t.Fatal("Highlight returned empty string")
	}
	found := false
	for i := 0; i <= len(got)-5; i++ {
		if got[i:i+5] == "brown" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Highlight result %q does not contain 'brown'", got)
	}
}

func TestHighlight_NoMatch(t *testing.T) {
	text := "hello world"
	got := Highlight(text, []string{"xyz"}, 100)
	if got != text {
		t.Errorf("Highlight no match = %q, want %q (original text)", got, text)
	}
}

func TestHighlight_Truncation(t *testing.T) {
	text := "a very long piece of text that should be truncated when maxLen is small"
	got := Highlight(text, []string{"xyz"}, 10)
	if len(got) > 16 {
		t.Errorf("Highlight truncation = %d chars, want <= 16", len(got))
	}
	if len(got) >= 3 && got[len(got)-3:] != "..." {
		t.Errorf("Highlight truncation should end with '...', got %q", got)
	}
}

func TestHighlightAll(t *testing.T) {
	text := "the cat sat on the mat"
	got := HighlightAll(text, []string{"cat", "mat"})
	want := "the <b>cat</b> sat on the <b>mat</b>"
	if got != want {
		t.Errorf("HighlightAll = %q, want %q", got, want)
	}
}
