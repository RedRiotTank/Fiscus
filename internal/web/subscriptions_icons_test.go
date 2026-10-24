package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/pkg/icons"
)

func TestBrandIconResolutionAndServing(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	tests := map[string]string{
		"SPOTIFY AB":               "/static/icons/brands/spotify.svg",
		"AMAZON WEB SERVICES EMEA": "/static/icons/brands/aws.svg",
		"Amazon Prime Video":       "/static/icons/brands/primevideo.svg",
		"GITHUB SPONSORS":          "/static/icons/brands/github.svg",
		"Google Workspace":         "/static/icons/brands/googleworkspace.svg",
		"Microsoft 365":            "/static/icons/brands/microsoft.svg",
		"Bookkeeping Services":     "",
	}
	for merchant, want := range tests {
		if got := f.server.brandIcon(merchant); got != want {
			t.Errorf("brandIcon(%q) = %q, want %q", merchant, got, want)
		}
	}
	for _, slug := range []string{"netflix", "spotify", "aws", "github", "jetbrains", "cloudflare", "vodafone", "adobe", "microsoft", "slack", "notion", "1password", "basicfit", "strava", "audible"} {
		if _, ok := f.server.brandIcons[slug]; !ok {
			t.Errorf("missing embedded brand icon %q", slug)
		}
	}

	icon := f.request(t, http.MethodGet, "/static/icons/brands/spotify.svg", true, nil)
	if icon.Code != http.StatusOK {
		t.Fatalf("icon status = %d", icon.Code)
	}
	if contentType := icon.Header().Get("Content-Type"); !strings.Contains(contentType, "image/svg+xml") {
		t.Fatalf("icon content type = %q", contentType)
	}
	if !strings.Contains(icon.Body.String(), "<svg") {
		t.Fatalf("icon body is not SVG: %s", icon.Body.String())
	}
	page := f.request(t, http.MethodGet, "/subscriptions", true, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("subscriptions status = %d", page.Code)
	}
	if csp := page.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "img-src 'self' data:") {
		t.Fatalf("CSP does not allow same-origin SVGs: %q", csp)
	}
}

func TestBrandIconOverridesAndFallback(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	f.server.iconResolver = icons.New(map[string]string{
		"Mi Gestoria SL":  "briefcase",
		"Custom Provider": "custom-brand",
	})
	if got := f.server.brandIcon("Mi Gestoría S.L."); got != "/static/icons/brands/briefcase.svg" {
		t.Fatalf("override icon = %q", got)
	}
	if got := f.server.brandIcon("Custom Provider Europe"); got != "" {
		t.Fatalf("missing override file should fall back, got %q", got)
	}
}

func TestSubscriptionsPageRendersBrandIcon(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	ctx := context.Background()
	for i, date := range []time.Time{
		time.Date(2026, 1, 20, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC),
	} {
		tx := domain.Transaction{
			AccountID: "acc-1", ExternalID: "spotify-" + date.Format("200601"),
			AmountMinor: -1_099, Currency: "EUR", BookingDate: date,
			Counterparty: "Spotify AB", Description: "Spotify Premium",
			Category: "Subscriptions", Status: domain.TransactionStatusBooked,
		}
		if _, _, err := f.store.UpsertTransaction(ctx, &tx); err != nil {
			t.Fatalf("UpsertTransaction %d: %v", i, err)
		}
	}
	page := f.request(t, http.MethodGet, "/subscriptions", true, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("subscriptions status = %d %s", page.Code, page.Body.String())
	}
	body := page.Body.String()
	for _, want := range []string{
		"Spotify AB",
		"/static/icons/brands/spotify.svg",
		"service-avatar service-brand",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("subscriptions page missing %q", want)
		}
	}
	if strings.Contains(body, `aria-hidden="true">S</span>`) {
		t.Error("Spotify still rendered an initials avatar")
	}
}
