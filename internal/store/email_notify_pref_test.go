package store

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestParseEmailNotifyPref(t *testing.T) {
	defaults := DefaultEmailNotifyPref()
	tests := []struct {
		name string
		raw  string
		want EmailNotifyPref
	}{
		{name: "unset", raw: "", want: defaults},
		{name: "empty object", raw: `{}`, want: defaults},
		{name: "missing version migrates without creator opt in", raw: `{"enabled":true}`, want: EmailNotifyPref{V: 2, Enabled: true, Assigned: true, AddedToProject: true}},
		{name: "partial v1 migrates without creator opt in", raw: `{"v":1,"cardActivity":true}`, want: EmailNotifyPref{V: 2, Assigned: true, CardActivity: true, AddedToProject: true}},
		{name: "numeric v1", raw: `{"v":1.0}`, want: defaults},
		{name: "explicit false", raw: `{"v":1,"assigned":false,"addedToProject":false}`, want: EmailNotifyPref{V: 2}},
		{name: "complete v1 migrates", raw: `{"v":1,"enabled":true,"assigned":false,"cardActivity":true,"sprintActivity":true,"projectActivity":true,"addedToProject":false}`, want: EmailNotifyPref{V: 2, Enabled: true, CardActivity: true, SprintActivity: true, ProjectActivity: true}},
		{name: "v2 explicit creator opt in", raw: `{"v":2,"enabled":true,"createdByMe":true}`, want: EmailNotifyPref{V: 2, Enabled: true, Assigned: true, CreatedByMe: true, AddedToProject: true}},
		{name: "v2 absent creator remains disabled", raw: `{"v":2,"enabled":true}`, want: EmailNotifyPref{V: 2, Enabled: true, Assigned: true, AddedToProject: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseEmailNotifyPref(tt.raw)
			if err != nil {
				t.Fatalf("ParseEmailNotifyPref: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseEmailNotifyPrefRejectsInvalidJSON(t *testing.T) {
	tests := []string{
		`null`,
		`[]`,
		`not json`,
		`{"v":3}`,
		`{"v":null}`,
		`{"v":"1"}`,
		`{"v":1.5}`,
		`{"enabled":"true"}`,
		`{"assigned":null}`,
		`{"v":1,"createdByMe":true}`,
		`{"createdByMe":true}`,
		`{"unknown":true}`,
	}
	for _, raw := range tests {
		if _, err := ParseEmailNotifyPref(raw); !errors.Is(err, ErrValidation) {
			t.Fatalf("%q: expected ErrValidation, got %v", raw, err)
		}
	}
}

func TestEmailNotifyPrefCanonicalJSON(t *testing.T) {
	raw, err := json.Marshal(DefaultEmailNotifyPref())
	if err != nil {
		t.Fatal(err)
	}
	want := `{"v":2,"enabled":false,"assigned":true,"createdByMe":false,"cardActivity":false,"sprintActivity":false,"projectActivity":false,"addedToProject":true}`
	if string(raw) != want {
		t.Fatalf("got %s, want %s", raw, want)
	}
}

func TestValidateEmailNotifyPrefJSON(t *testing.T) {
	if err := ValidateEmailNotifyPrefJSON(`{"enabled":true}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := ValidateEmailNotifyPrefJSON(`{"v":2,"enabled":true,"createdByMe":true}`); err != nil {
		t.Fatalf("unexpected v2 error: %v", err)
	}
	if err := ValidateEmailNotifyPrefJSON(`{"extra":true}`); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
}
