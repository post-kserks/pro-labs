package fts

// Corpus holds document-level statistics needed for BM25 scoring.
type Corpus struct {
	TotalDocs int
	AvgDocLen float64
	DocFreq   map[string]int // term -> number of documents containing it
}

// NewCorpus creates an empty corpus.
func NewCorpus() *Corpus {
	return &Corpus{
		DocFreq: make(map[string]int),
	}
}

// IndexDoc adds a document's token list to the corpus statistics.
func (c *Corpus) IndexDoc(docTerms []string) {
	c.TotalDocs++
	c.AvgDocLen = (c.AvgDocLen*float64(c.TotalDocs-1) + float64(len(docTerms))) / float64(c.TotalDocs)

	seen := make(map[string]bool)
	for _, t := range docTerms {
		if !seen[t] {
			c.DocFreq[t]++
			seen[t] = true
		}
	}
}

// ScoreDocument returns the BM25 score for docTerms against queryTerms
// using this corpus's statistics.
func (c *Corpus) ScoreDocument(docTerms, queryTerms []string) float64 {
	scorer := NewBM25()
	if c.TotalDocs == 0 {
		return 0
	}
	return scorer.Score(docTerms, queryTerms, c.AvgDocLen, c.DocFreq, c.TotalDocs)
}
