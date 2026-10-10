package httpapi

import (
	"context"
	"testing"

	"scrumboy/internal/store"
)

func TestPublicSlugReservationsStaySynchronizedWithRoutesAndLandingLocales(t *testing.T) {
	st := newTestStore(t)
	server := NewServer(st, Options{ScrumboyMode: "full"})
	t.Cleanup(func() { server.Close(context.Background()) })

	for _, slug := range []string{"_app", "agora", "anon", "api", "auth", "dashboard", "healthz", "mcp", "oauth", "p", "temp", "en", "pseudo"} {
		if !store.IsReservedProjectSlug(slug) {
			t.Fatalf("application path %q is not reserved", slug)
		}
	}
	for locale := range server.landingHTMLByLocale {
		if !store.IsReservedProjectSlug(locale) {
			t.Fatalf("generated landing locale %q is not reserved", locale)
		}
	}
	for _, slug := range []string{"ignite", "roadmap", "public-roadmap"} {
		if store.IsReservedProjectSlug(slug) {
			t.Fatalf("ordinary public slug %q is reserved", slug)
		}
	}
}
