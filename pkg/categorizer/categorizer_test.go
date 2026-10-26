package categorizer

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTokenizeSanitizesMerchantText(t *testing.T) {
	t.Parallel()
	tests := map[string][]string{
		"1.08 Monster":            {"monster"},
		"Eweka Internet Services": {"eweka", "internet", "services"},
		"For Uber Eats":           {"for", "uber", "eats"},
		"El Corte Inglés":         {"el", "corte", "ingles"},
		"  Mercadona   S.A.  ":    {"mercadona"},
	}
	for input, want := range tests {
		got := Tokenize(input)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("Tokenize(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestAmountBuckets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		amount int64
		want   string
	}{
		{-999, featureAmount + "lt10"},
		{-1_000, featureAmount + "10to50"},
		{-4_999, featureAmount + "10to50"},
		{-5_000, featureAmount + "50to200"},
		{-19_999, featureAmount + "50to200"},
		{-20_000, featureAmount + "gt200"},
		{0, featureAmount + "unknown"},
	}
	for _, test := range tests {
		if got := AmountBucket(test.amount); got != test.want {
			t.Errorf("AmountBucket(%d) = %q, want %q", test.amount, got, test.want)
		}
	}
}

func TestNaiveBayesPredictsWithHighConfidence(t *testing.T) {
	t.Parallel()
	model := NewNaiveBayes(1)
	for i := 0; i < 5; i++ {
		model.Train("Mercadona compra semanal", CategoryGroceries, -2_500, 1)
		model.Train("Glovo cena restaurante", CategoryRestaurants, -2_200, 1)
		model.Train("Renfe metro abono", CategoryTransport, -2_000, 1)
	}
	prediction, ok := model.Predict("Mercadona compra", -2_400)
	if !ok {
		t.Fatal("expected a prediction")
	}
	if prediction.Category != CategoryGroceries {
		t.Fatalf("prediction = %+v, want %s", prediction, CategoryGroceries)
	}
	if prediction.Confidence < DefaultThreshold {
		t.Fatalf("confidence = %.3f, want >= %.2f", prediction.Confidence, DefaultThreshold)
	}
}

func TestNaiveBayesPredictionIsFast(t *testing.T) {
	t.Parallel()
	model := NewNaiveBayes(1)
	for i := 0; i < 10; i++ {
		model.Train("Mercadona compra semanal", CategoryGroceries, -2_500, 1)
		model.Train("Glovo cena restaurante", CategoryRestaurants, -2_200, 1)
		model.Train("Renfe metro abono", CategoryTransport, -2_000, 1)
	}
	start := time.Now()
	for i := 0; i < 2_000; i++ {
		if _, ok := model.Predict("Mercadona compra semanal", -2_400); !ok {
			t.Fatal("expected a prediction")
		}
	}
	elapsed := time.Since(start)
	if elapsed > 2*time.Second {
		t.Fatalf("2000 predictions took %s; expected microsecond-scale execution", elapsed)
	}
}

func TestCascadeCatalogDetectsEwekaSubscription(t *testing.T) {
	t.Parallel()
	cascade, err := NewCascade(Config{})
	if err != nil {
		t.Fatalf("NewCascade: %v", err)
	}
	result := cascade.Classify(Input{Counterparty: "Eweka Internet Services", AmountMinor: -950})
	if result.Category != CategorySubscriptions || result.Tier != TierExact {
		t.Fatalf("result = %+v, want exact Subscriptions", result)
	}
	if result.Confidence < 1 {
		t.Fatalf("confidence = %v, want deterministic confidence", result.Confidence)
	}
}

func TestCascadeMonsterRoutesToGroceriesNotHousing(t *testing.T) {
	t.Parallel()
	cascade, err := NewCascade(Config{})
	if err != nil {
		t.Fatalf("NewCascade: %v", err)
	}
	result := cascade.Classify(Input{Counterparty: "1.08 Monster", AmountMinor: -108})
	if result.Category != CategoryGroceries {
		t.Fatalf("result = %+v, want Groceries", result)
	}
	if strings.EqualFold(result.Category, CategoryHousing) {
		t.Fatalf("energy drink leaked into housing: %+v", result)
	}
}

func TestCascadeOnlineLearningInheritsCategory(t *testing.T) {
	t.Parallel()
	cascade, err := NewCascade(Config{Catalog: []CatalogEntry{}})
	if err != nil {
		t.Fatalf("NewCascade: %v", err)
	}
	cascade.Seed(Input{Counterparty: "Netflix", AmountMinor: -999}, CategorySubscriptions, 1)
	cascade.Seed(Input{Counterparty: "Renfe", AmountMinor: -2_000}, CategoryTransport, 1)
	unknown := Input{Counterparty: "Coviran", AmountMinor: -2_500}
	first := cascade.Classify(unknown)
	if first.Tier != TierFallback || first.Category != CategoryUncategorized {
		t.Fatalf("before learning = %+v, want conservative fallback", first)
	}
	cascade.Learn(unknown, CategoryGroceries)
	second := cascade.Classify(unknown)
	if second.Category != CategoryGroceries || second.Confidence < DefaultThreshold {
		t.Fatalf("after learning = %+v, want high-confidence Groceries", second)
	}
	prediction, ok := cascade.bayes.Predict(unknown.text(), unknown.AmountMinor)
	if !ok || prediction.Category != CategoryGroceries || prediction.Confidence < DefaultThreshold {
		t.Fatalf("bayes prediction = %+v (ok=%v)", prediction, ok)
	}
}

func TestCascadeLowConfidenceFallback(t *testing.T) {
	t.Parallel()
	cascade, err := NewCascade(Config{})
	if err != nil {
		t.Fatalf("NewCascade: %v", err)
	}
	result := cascade.Classify(Input{Counterparty: "Zzz Qqq", AmountMinor: -2_500})
	if result.Tier != TierFallback || result.Category != CategoryUncategorized {
		t.Fatalf("unknown merchant = %+v, want Uncategorized fallback", result)
	}
}

func TestCascadeExplicitRulesWin(t *testing.T) {
	t.Parallel()
	cascade, err := NewCascade(Config{Rules: []Rule{{Category: "Business Meals", Pattern: `(?i)glovo`}}})
	if err != nil {
		t.Fatalf("NewCascade: %v", err)
	}
	result := cascade.Classify(Input{Counterparty: "Glovo", AmountMinor: -1_500})
	if result.Category != "Business Meals" || result.Tier != TierExact {
		t.Fatalf("result = %+v, want explicit rule", result)
	}
}

func TestCascadeRoutesSavingsTransfersToSavingsPillar(t *testing.T) {
	t.Parallel()
	cascade, err := NewCascade(Config{})
	if err != nil {
		t.Fatalf("NewCascade: %v", err)
	}
	outflow := cascade.Classify(Input{Counterparty: "To Cuenta Remunerada", AmountMinor: -100_000})
	if outflow.Category != CategorySavings || outflow.Tier != TierExact {
		t.Fatalf("savings outflow = %+v, want Savings & Investments", outflow)
	}
	inflow := cascade.Classify(Input{Counterparty: "From Cuenta Remunerada", AmountMinor: 25_000})
	if inflow.Category != "Transfers / Pass-through" || inflow.Tier != TierExact {
		t.Fatalf("savings inflow = %+v, want neutral pass-through", inflow)
	}
}

func TestCascadeBizumUsesCanonicalP2PCategory(t *testing.T) {
	t.Parallel()
	cascade, err := NewCascade(Config{})
	if err != nil {
		t.Fatalf("NewCascade: %v", err)
	}
	result := cascade.Classify(Input{Counterparty: "Bizum Juan", AmountMinor: -1_500})
	if result.Category != CategoryP2P {
		t.Fatalf("Bizum category = %q, want %q", result.Category, CategoryP2P)
	}
}

func TestCascadeP2PDiningConceptsRouteToRestaurants(t *testing.T) {
	t.Parallel()
	cascade, err := NewCascade(Config{})
	if err != nil {
		t.Fatalf("NewCascade: %v", err)
	}
	tests := []Input{
		{Counterparty: "Bizum Juan", Description: "cena viernes", AmountMinor: -2_000},
		{Counterparty: "Bizum Luis", Description: "Burger King", AmountMinor: -1_500},
		{Counterparty: "John", Description: "For Uber Eats | To John", AmountMinor: -1_800},
		{Counterparty: "Bizum Ana", Description: "pizza y tapas", AmountMinor: -1_200},
		{Counterparty: "Bizum Ana", Description: "brunch domingo", AmountMinor: -2_400},
	}
	for _, input := range tests {
		result := cascade.Classify(input)
		if result.Category != CategoryRestaurants || result.Tier != TierExact {
			t.Errorf("Classify(%+v) = %+v, want exact Restaurants & Delivery", input, result)
		}
	}
	inbound := cascade.Classify(Input{Counterparty: "John", Description: "cena viernes | From John", AmountMinor: 2_000})
	if inbound.Category == CategoryRestaurants {
		t.Fatalf("inbound transfer was classified as dining: %+v", inbound)
	}
}

func TestCascadeConcurrentLearnAndClassify(t *testing.T) {
	t.Parallel()
	cascade, err := NewCascade(Config{})
	if err != nil {
		t.Fatalf("NewCascade: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cascade.Learn(Input{Counterparty: "Panaderia Luna", AmountMinor: -350}, CategoryGroceries)
			_ = cascade.Classify(Input{Counterparty: "Panaderia Luna", AmountMinor: -350})
		}()
	}
	wg.Wait()
}

func BenchmarkNaiveBayesPredict(b *testing.B) {
	model := NewNaiveBayes(1)
	for i := 0; i < 20; i++ {
		model.Train("Mercadona compra semanal", CategoryGroceries, -2_500, 1)
		model.Train("Glovo cena restaurante", CategoryRestaurants, -2_200, 1)
		model.Train("Renfe metro abono", CategoryTransport, -2_000, 1)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = model.Predict("Mercadona compra semanal", -2_400)
	}
}
