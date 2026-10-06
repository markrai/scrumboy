package project

import (
	"context"
	"reflect"
	"testing"
	"time"
)

type projectServiceTrace struct {
	steps []string
}

func (t *projectServiceTrace) add(step string) {
	t.steps = append(t.steps, step)
}

func assertProjectServiceTrace(t *testing.T, trace *projectServiceTrace, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(trace.steps, want) {
		t.Fatalf("trace = %v, want %v", trace.steps, want)
	}
}

func projectServiceString(value string) *string {
	return &value
}

func projectServiceInt(value int) *int {
	return &value
}

func projectServiceInt64(value int64) *int64 {
	return &value
}

func projectServiceTime(value time.Time) *time.Time {
	return &value
}

func assertProjectServiceContext(t *testing.T, got, want context.Context) {
	t.Helper()
	if got != want {
		t.Fatalf("context identity changed: got %p want %p", got, want)
	}
}
