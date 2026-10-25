package subscriptions

import "testing"

func TestCleanMerchant(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"Amazon Prime*nk9ae7fn4":        "Amazon Prime",
		"Amazon Prime*nj7t039f4":        "Amazon Prime",
		"Www.amazon*abc123":             "Amazon",
		"Google G1ai101m":               "Google",
		"Google One":                    "Google",
		"Spotify AB":                    "Spotify",
		"Netflix.com":                   "Netflix",
		"AMAZON WEB SERVICES EMEA SARL": "Amazon Web Services",
		"Mi Gestoría S.L.":              "Mi Gestoría",
		"  MERCADONA  ":                 "MERCADONA",
	}
	for input, want := range tests {
		if got := CleanMerchant(input); got != want {
			t.Errorf("CleanMerchant(%q) = %q, want %q", input, got, want)
		}
	}
	if got := CleanMerchant(""); got != "" {
		t.Errorf("CleanMerchant(empty) = %q", got)
	}
}

func TestMerchantKey(t *testing.T) {
	t.Parallel()
	if MerchantKey("Amazon Prime*ABC123") != "amazon prime" {
		t.Fatalf("key = %q", MerchantKey("Amazon Prime*ABC123"))
	}
	if MerchantKey("Google One") != "google" {
		t.Fatalf("google key = %q", MerchantKey("Google One"))
	}
}
