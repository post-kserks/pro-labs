package fts

import "strings"

// Tokenize splits text into lowercase alphanumeric tokens.
func Tokenize(text string) []string {
	text = strings.ToLower(text)
	var tokens []string
	var current strings.Builder
	for _, r := range text {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			current.WriteRune(r)
		} else {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

// StopWords is a set of common English words to ignore in indexing and search.
var StopWords = map[string]bool{
	"the": true, "a": true, "an": true, "is": true, "are": true,
	"was": true, "were": true, "be": true, "been": true, "being": true,
	"have": true, "has": true, "had": true, "do": true, "does": true,
	"did": true, "will": true, "would": true, "could": true, "should": true,
	"may": true, "might": true, "shall": true, "can": true, "need": true,
	"it": true, "its": true, "he": true, "she": true, "they": true,
	"we": true, "you": true, "i": true, "me": true, "my": true,
}

// TokenizeNoStop tokenizes text and removes stop words.
func TokenizeNoStop(text string) []string {
	all := Tokenize(text)
	var result []string
	for _, t := range all {
		if !StopWords[t] {
			result = append(result, t)
		}
	}
	return result
}
