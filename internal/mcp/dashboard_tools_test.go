package mcp

import "testing"

func TestNormalizeDashboardTodosLimit_nilDefaultsTo20(t *testing.T) {
	t.Parallel()
	limit, aerr := normalizeDashboardTodosLimit(nil)
	if aerr != nil {
		t.Fatalf("normalizeDashboardTodosLimit: %v", aerr)
	}
	if limit != 20 {
		t.Fatalf("expected default limit 20, got %d", limit)
	}
}

func TestNormalizeDashboardTodosLimit_validValue(t *testing.T) {
	t.Parallel()
	v := 50
	limit, aerr := normalizeDashboardTodosLimit(&v)
	if aerr != nil {
		t.Fatalf("normalizeDashboardTodosLimit: %v", aerr)
	}
	if limit != 50 {
		t.Fatalf("expected limit 50, got %d", limit)
	}
}

func TestNormalizeDashboardTodosLimit_zeroRejected(t *testing.T) {
	t.Parallel()
	v := 0
	_, aerr := normalizeDashboardTodosLimit(&v)
	if aerr == nil {
		t.Fatal("expected adapter error for limit 0")
	}
	if aerr.Code != CodeValidationError {
		t.Fatalf("expected validation error code, got %q", aerr.Code)
	}
}

func TestNormalizeDashboardTodosLimit_tooLargeRejected(t *testing.T) {
	t.Parallel()
	v := 101
	_, aerr := normalizeDashboardTodosLimit(&v)
	if aerr == nil {
		t.Fatal("expected adapter error for limit > 100")
	}
	if aerr.Code != CodeValidationError {
		t.Fatalf("expected validation error code, got %q", aerr.Code)
	}
}
