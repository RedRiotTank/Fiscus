package icons

import "testing"

func TestNormalize(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"  SPOTIFY AB! ":            "spotify ab",
		"Amazon Web Services, EMEA": "amazon web services emea",
		"Mi Gestoría S.L.":          "mi gestoria sl",
		"1Password — Family":        "1password family",
		"Basic-Fit / Madrid":        "basic fit madrid",
	}
	for input, want := range tests {
		if got := Normalize(input); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestResolveBrands(t *testing.T) {
	t.Parallel()
	resolver := New(nil)
	tests := map[string]string{
		"SPOTIFY AB":               "spotify",
		"AMAZON WEB SERVICES EMEA": "aws",
		"Amazon Prime Video":       "primevideo",
		"GITHUB SPONSORS":          "github",
		"GitHub Copilot":           "github",
		"Microsoft 365":            "microsoft",
		"Google Workspace":         "googleworkspace",
		"Google One":               "google",
		"YouTube Premium":          "youtube",
		"Max":                      "hbomax",
		"HBO Max":                  "hbomax",
		"Disney+":                  "disneyplus",
		"Basic-Fit":                "basicfit",
		"1Password Families":       "1password",
		"Digital Ocean":            "digitalocean",
		"Movistar Fibra":           "movistar",
		"Endesa Energía":           "endesa",
		"Iberdrola Spain":          "iberdrola",
		"Vodafone España":          "vodafone",
		"Adobe Creative Cloud":     "adobe",
		"Notion Labs":              "notion",
		"Strava Subscription":      "strava",
		"Audible GmbH":             "audible",
		"Figma professional":       "figma",
		"Namecheap Domain":         "namecheap",
		"Stripe Payments":          "stripe",
	}
	for merchant, want := range tests {
		got, ok := resolver.Resolve(merchant)
		if !ok || got != want {
			t.Errorf("Resolve(%q) = %q, %v; want %q, true", merchant, got, ok, want)
		}
	}
}

func TestResolveOverrides(t *testing.T) {
	t.Parallel()
	resolver := New(map[string]string{
		"Mi Gestoria SL":  "briefcase",
		"Custom Provider": "/static/icons/brands/custom-brand.svg",
		"Evil":            "https://example.test/icon.svg",
		"Traversal":       "../secret",
	})
	if got, ok := resolver.Resolve("Mi Gestoría S.L."); !ok || got != "briefcase" {
		t.Fatalf("override = %q, %v", got, ok)
	}
	if got, ok := resolver.Resolve("Custom Provider Europe"); !ok || got != "custom-brand" {
		t.Fatalf("path override = %q, %v", got, ok)
	}
	if _, ok := resolver.Resolve("Evil Corp"); ok {
		t.Fatal("external override should be ignored")
	}
	if _, ok := resolver.Resolve("Traversal SL"); ok {
		t.Fatal("path traversal override should be ignored")
	}
}

func TestResolveFallback(t *testing.T) {
	t.Parallel()
	resolver := New(nil)
	if got, ok := resolver.Resolve("Unknown Local Shop"); ok || got != "" {
		t.Fatalf("fallback = %q, %v", got, ok)
	}
	if got, ok := resolver.Resolve(""); ok || got != "" {
		t.Fatalf("empty fallback = %q, %v", got, ok)
	}
}

func TestDemoSubscriptionsResolve(t *testing.T) {
	t.Parallel()
	resolver := New(nil)
	merchants := map[string]string{
		"Netflix":             "netflix",
		"Spotify":             "spotify",
		"Vodafone":            "vodafone",
		"GitHub Copilot":      "github",
		"Figma":               "figma",
		"Amazon Web Services": "aws",
		"Cloudflare":          "cloudflare",
		"JetBrains":           "jetbrains",
		"Adobe":               "adobe",
		"Apple":               "apple",
		"Namecheap":           "namecheap",
		"Stripe":              "stripe",
	}
	for merchant, want := range merchants {
		got, ok := resolver.Resolve(merchant)
		if !ok || got != want {
			t.Errorf("demo Resolve(%q) = %q, %v; want %q", merchant, got, ok, want)
		}
	}
}
