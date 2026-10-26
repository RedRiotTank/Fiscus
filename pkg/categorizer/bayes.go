package categorizer

import (
	"math"
	"sort"
	"strings"
	"sync"
)

// DefaultThreshold is the minimum posterior probability required before the
// Bayesian tier assigns a category automatically.
const DefaultThreshold = 0.82

// NaiveBayes is a small multinomial naive Bayes classifier. It is deliberately
// in-memory and dependency-free: no CGO, no external runtime, and a footprint
// measured in kilobytes for typical household transaction volumes.
type NaiveBayes struct {
	mu sync.RWMutex

	alpha       float64
	documents   map[string]float64
	tokens      map[string]map[string]float64
	tokenTotals map[string]float64
	vocabulary  map[string]struct{}
}

// NewNaiveBayes constructs an empty model. alpha is the Laplace smoothing
// factor and defaults to one when non-positive.
func NewNaiveBayes(alpha float64) *NaiveBayes {
	if alpha <= 0 {
		alpha = 1
	}
	return &NaiveBayes{
		alpha:       alpha,
		documents:   make(map[string]float64),
		tokens:      make(map[string]map[string]float64),
		tokenTotals: make(map[string]float64),
		vocabulary:  make(map[string]struct{}),
	}
}

// Train adds one example to the model. weight lets deterministic catalog seeds
// and explicit user corrections count more strongly than ambient history.
func (n *NaiveBayes) Train(text, category string, amountMinor int64, weight float64) {
	if n == nil {
		return
	}
	category = strings.TrimSpace(category)
	if category == "" {
		return
	}
	if weight <= 0 {
		weight = 1
	}
	features := Features(text, amountMinor)
	n.mu.Lock()
	defer n.mu.Unlock()
	n.documents[category] += weight
	if n.tokens[category] == nil {
		n.tokens[category] = make(map[string]float64)
	}
	seen := make(map[string]struct{}, len(features))
	for _, feature := range features {
		if _, duplicate := seen[feature]; duplicate {
			continue
		}
		seen[feature] = struct{}{}
		n.tokens[category][feature] += weight
		n.tokenTotals[category] += weight
		n.vocabulary[feature] = struct{}{}
	}
}

// Prediction is the best category and its normalized posterior probability.
type Prediction struct {
	Category   string
	Confidence float64
}

// Predict returns the most likely category. A false result means the model has
// no training data at all.
func (n *NaiveBayes) Predict(text string, amountMinor int64) (Prediction, bool) {
	if n == nil {
		return Prediction{}, false
	}
	features := Features(text, amountMinor)
	n.mu.RLock()
	defer n.mu.RUnlock()
	if len(n.documents) == 0 || len(n.vocabulary) == 0 {
		return Prediction{}, false
	}

	categories := make([]string, 0, len(n.documents))
	totalDocuments := 0.0
	for category, count := range n.documents {
		if count <= 0 {
			continue
		}
		categories = append(categories, category)
		totalDocuments += count
	}
	if len(categories) == 0 || totalDocuments <= 0 {
		return Prediction{}, false
	}
	sort.Strings(categories)
	vocabularySize := float64(len(n.vocabulary))

	logScores := make([]float64, 0, len(categories))
	bestIndex := 0
	bestScore := math.Inf(-1)
	for _, category := range categories {
		score := math.Log(n.documents[category] / totalDocuments)
		denominator := n.tokenTotals[category] + n.alpha*vocabularySize
		if denominator <= 0 {
			denominator = n.alpha
		}
		counts := n.tokens[category]
		for _, feature := range features {
			count := counts[feature]
			score += math.Log((count + n.alpha) / denominator)
		}
		logScores = append(logScores, score)
	}
	for i, score := range logScores {
		if score > bestScore {
			bestScore = score
			bestIndex = i
		}
	}
	// Stable log-sum-exp for a numerically safe normalization.
	total := 0.0
	for _, score := range logScores {
		total += math.Exp(score - bestScore)
	}
	confidence := 1.0
	if total > 1 {
		confidence = 1 / total
	}
	if confidence < 0 || confidence > 1 || math.IsNaN(confidence) {
		confidence = 0
	}
	return Prediction{Category: categories[bestIndex], Confidence: confidence}, true
}

// Categories returns every category with at least one training example.
func (n *NaiveBayes) Categories() []string {
	if n == nil {
		return nil
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	out := make([]string, 0, len(n.documents))
	for category, count := range n.documents {
		if count > 0 {
			out = append(out, category)
		}
	}
	sort.Strings(out)
	return out
}

// Examples returns the weighted number of training documents.
func (n *NaiveBayes) Examples() float64 {
	if n == nil {
		return 0
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	total := 0.0
	for _, count := range n.documents {
		total += count
	}
	return total
}
