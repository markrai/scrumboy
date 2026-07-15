package store

import (
	"errors"
	"testing"
)

func TestParseEmailNotifyPref(t *testing.T) {
	p, err := ParseEmailNotifyPref("")
	if err != nil {
		t.Fatalf("empty: unexpected error %v", err)
	}
	if !p.Assigned || !p.AddedToProject || p.Enabled || p.CardActivity || p.SprintActivity || p.ProjectActivity {
		t.Fatalf("empty: unexpected defaults %+v", p)
	}

	p, err = ParseEmailNotifyPref(`{"v":1,"enabled":true,"assigned":true,"cardActivity":true}`)
	if err != nil {
		t.Fatalf("full: unexpected error %v", err)
	}
	if !p.Enabled || !p.Assigned || !p.CardActivity {
		t.Fatalf("full: unexpected parse %+v", p)
	}
	if p.SprintActivity || p.ProjectActivity || p.AddedToProject {
		t.Fatalf("full: unset fields should be false %+v", p)
	}

	if _, err := ParseEmailNotifyPref(`not json`); !errors.Is(err, ErrValidation) {
		t.Fatalf("bad json: expected ErrValidation, got %v", err)
	}

	if _, err := ParseEmailNotifyPref(`{"v":99,"enabled":true}`); !errors.Is(err, ErrValidation) {
		t.Fatalf("bad version: expected ErrValidation, got %v", err)
	}
}

func TestValidateEmailNotifyPrefJSON(t *testing.T) {
	if err := ValidateEmailNotifyPrefJSON(`{"v":1,"enabled":true}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := ValidateEmailNotifyPrefJSON(`{`); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}
