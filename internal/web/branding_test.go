package web

import (
	"net/http"
	"strings"
	"testing"
)

func TestBrandingAssetsAreServed(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	page := f.request(t, http.MethodGet, "/", true, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d", page.Code)
	}
	body := page.Body.String()
	for _, want := range []string{
		`href="/static/favicon.png"`,
		`href="/static/favicon.ico"`,
		`href="/static/apple-touch-icon.png"`,
		`class="brand-logo"`,
		`src="/static/brand.png"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard branding missing %q", want)
		}
	}
	if strings.Contains(body, `rel="icon" href="data:`) {
		t.Error("dashboard still uses the old inline favicon")
	}
	for _, want := range []string{
		`href="https://github.com/RedRiotTank/Fiscus"`,
		`target="_blank"`,
		`rel="noopener noreferrer"`,
		`RedRiotTank/Fiscus`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard source attribution missing %q", want)
		}
	}

	for _, path := range []string{
		"/static/brand.png",
		"/static/favicon.png",
		"/static/apple-touch-icon.png",
	} {
		asset := f.request(t, http.MethodGet, path, true, nil)
		if asset.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, asset.Code)
		}
		if contentType := asset.Header().Get("Content-Type"); !strings.Contains(contentType, "image/png") {
			t.Errorf("%s content type = %q", path, contentType)
		}
	}
	icon := f.request(t, http.MethodGet, "/static/favicon.ico", true, nil)
	if icon.Code != http.StatusOK {
		t.Fatalf("favicon.ico status = %d", icon.Code)
	}
	if contentType := icon.Header().Get("Content-Type"); !strings.Contains(contentType, "icon") {
		t.Errorf("favicon.ico content type = %q", contentType)
	}
}
