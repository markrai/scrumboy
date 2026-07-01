package auth

import (
	"errors"
	"testing"

	"scrumboy/internal/errs"
)

func TestValidatePassword(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "empty", input: "", wantErr: true},
		{name: "whitespace only", input: " \t\n ", wantErr: true},
		{name: "below minimum", input: "1234567", wantErr: true},
		{name: "below minimum after trimming", input: " 1234567 ", wantErr: true},
		{name: "exactly minimum", input: "12345678"},
		{name: "longer password", input: "password123"},
		{name: "exactly minimum after trimming", input: " 12345678 "},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePassword(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if !errors.Is(err, errs.ErrValidation) {
					t.Fatalf("error = %v, want ErrValidation", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidatePassword(%q): %v", tc.input, err)
			}
		})
	}
}
