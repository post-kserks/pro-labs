package fts

import "math"

// BM25 implements the BM25 ranking algorithm.
type BM25 struct {
	k1 float64 // term frequency saturation (default 1.2)
	b  float64 // length normalization (default 0.75)
}

// NewBM25 creates a BM25 scorer with default parameters.
func NewBM25() *BM25 {
	return &BM25{k1: 1.2, b: 0.75}
}

// Score computes BM25 score for a document against a query.
//   - docTerms: tokenized document
//   - queryTerms: tokenized query
//   - avgDocLen: average document length in corpus (in tokens)
//   - docFreq: map term -> number of documents containing term
//   - totalDocs: total number of documents in the corpus
func (b *BM25) Score(docTerms, queryTerms []string, avgDocLen float64, docFreq map[string]int, totalDocs int) float64 {
	score := 0.0
	docLen := float64(len(docTerms))

	tf := make(map[string]int)
	for _, t := range docTerms {
		tf[t]++
	}

	for _, term := range queryTerms {
		df := float64(docFreq[term])
		if df == 0 {
			continue
		}

		// IDF component
		idf := math.Log((float64(totalDocs)-df+0.5)/(df+0.5) + 1)

		// TF component with length normalization
		termFreq := float64(tf[term])
		numerator := termFreq * (b.k1 + 1)
		denominator := termFreq + b.k1*(1-b.b+b.b*docLen/avgDocLen)

		score += idf * numerator / denominator
	}

	return score
}
