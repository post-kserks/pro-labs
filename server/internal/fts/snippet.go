package fts

import "strings"

// Highlight returns a snippet of text centered around the first match for any
// of the given query terms. Matches are wrapped in <b> tags. If no term is
// found, the beginning of the text is returned.
func Highlight(text string, queryTerms []string, maxLen int) string {
	lower := strings.ToLower(text)

	bestPos := -1
	for _, term := range queryTerms {
		pos := strings.Index(lower, term)
		if pos >= 0 && (bestPos < 0 || pos < bestPos) {
			bestPos = pos
		}
	}

	if bestPos < 0 {
		if len(text) > maxLen {
			return text[:maxLen] + "..."
		}
		return text
	}

	start := bestPos - maxLen/4
	if start < 0 {
		start = 0
	}
	end := start + maxLen
	if end > len(text) {
		end = len(text)
		start = end - maxLen
		if start < 0 {
			start = 0
		}
	}

	snippet := text[start:end]
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(text) {
		snippet = snippet + "..."
	}

	return snippet
}

// HighlightAll wraps every occurrence of each query term in <b> tags.
func HighlightAll(text string, queryTerms []string) string {
	result := text
	for _, term := range queryTerms {
		result = highlightTerm(result, term)
	}
	return result
}

func highlightTerm(text, term string) string {
	if len(term) == 0 {
		return text
	}
	lower := strings.ToLower(text)
	var buf strings.Builder
	i := 0
	for {
		pos := strings.Index(lower[i:], term)
		if pos < 0 {
			break
		}
		absPos := i + pos
		buf.WriteString(text[i:absPos])
		buf.WriteString("<b>")
		buf.WriteString(text[absPos : absPos+len(term)])
		buf.WriteString("</b>")
		i = absPos + len(term)
	}
	buf.WriteString(text[i:])
	return buf.String()
}
