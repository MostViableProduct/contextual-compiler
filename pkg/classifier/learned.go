package classifier

import "sync"

// LearnedKeyword represents a keyword discovered through LLM classification
// that can augment the static heuristic classifier.
type LearnedKeyword struct {
	Keyword              string  `json:"keyword"`
	Category             string  `json:"category"`
	Weight               float64 `json:"weight"`
	Confidence           float64 `json:"confidence"`
	TotalObservations    int     `json:"total_observations"`
	PositiveObservations int     `json:"positive_observations"`
}

// LearnedKeywordStore is a thread-safe runtime cache of promoted learned keywords.
type LearnedKeywordStore struct {
	mu       sync.RWMutex
	keywords []LearnedKeyword
}

// NewLearnedKeywordStore creates an empty store.
func NewLearnedKeywordStore() *LearnedKeywordStore {
	return &LearnedKeywordStore{}
}

// GetPromotedKeywords returns a map of category -> keyword list.
func (s *LearnedKeywordStore) GetPromotedKeywords() map[string][]string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string][]string)
	for _, kw := range s.keywords {
		result[kw.Category] = append(result[kw.Category], kw.Keyword)
	}
	return result
}

// GetPromotedWeights returns a map of keyword -> weight.
func (s *LearnedKeywordStore) GetPromotedWeights() map[string]float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]float64, len(s.keywords))
	for _, kw := range s.keywords {
		result[kw.Keyword] = kw.Weight
	}
	return result
}

// Update replaces the entire set of promoted keywords atomically.
func (s *LearnedKeywordStore) Update(keywords []LearnedKeyword) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keywords = keywords
}

// Count returns the number of currently promoted keywords.
func (s *LearnedKeywordStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.keywords)
}
