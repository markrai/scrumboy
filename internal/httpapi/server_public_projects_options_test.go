package httpapi

import (
	"context"
	"errors"
	"testing"

	publicboardapp "scrumboy/internal/application/publicboard"
)

func TestPublicBoardOptionsReachServerIndependentlyOfMode(t *testing.T) {
	st := newTestStore(t)
	tests := []struct {
		name           string
		mode           string
		publicEnabled  bool
		landingEnabled bool
	}{
		{name: "full defaults", mode: "full"},
		{name: "full public only", mode: "full", publicEnabled: true},
		{name: "full landing only", mode: "full", landingEnabled: true},
		{name: "anonymous both enabled", mode: "anonymous", publicEnabled: true, landingEnabled: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := NewServer(st, Options{
				ScrumboyMode:          tc.mode,
				PublicProjectsEnabled: tc.publicEnabled,
				LandingPageEnabled:    tc.landingEnabled,
			})
			t.Cleanup(func() { srv.Close(context.Background()) })

			if srv.mode != tc.mode {
				t.Fatalf("server mode = %q, want %q", srv.mode, tc.mode)
			}
			if srv.publicProjectsEnabled != tc.publicEnabled {
				t.Fatalf("server publicProjectsEnabled = %v, want %v", srv.publicProjectsEnabled, tc.publicEnabled)
			}
			if srv.landingPageEnabled != tc.landingEnabled {
				t.Fatalf("server landingPageEnabled = %v, want %v", srv.landingPageEnabled, tc.landingEnabled)
			}
			if srv.publicBoardPublications == nil {
				t.Fatal("publication application service was not constructed")
			}
			_, err := srv.publicBoardPublications.SetPublication(context.Background(), publicboardapp.PublicationCommand{ProjectID: 1, Enabled: true})
			if tc.mode != "full" || !tc.publicEnabled {
				if !errors.Is(err, publicboardapp.ErrPublicationCapabilityDisabled) {
					t.Fatalf("publication gate error = %v, want capability disabled", err)
				}
			} else if !errors.Is(err, publicboardapp.ErrActorRequired) {
				t.Fatalf("publication gate error = %v, want actor required after enabled gate", err)
			}
		})
	}
}
