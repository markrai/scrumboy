package board

import (
	"fmt"
	"reflect"
	"testing"
)

func TestNormalizeTagFilters(t *testing.T) {
	got, err := NormalizeTagFilters([]string{" Bug ", "feature", "bug", "", "  ", "Needs QA"})
	want := []string{"Bug", "feature", "Needs QA"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeTagFilters()=%v err=%v want=%v", got, err, want)
	}

	twenty := make([]string, MaxTagFilters)
	for i := range twenty {
		twenty[i] = fmt.Sprintf("tag-%d", i)
	}
	if got, err := NormalizeTagFilters(append(append([]string{}, twenty...), "TAG-0")); err != nil || len(got) != MaxTagFilters {
		t.Fatalf("duplicate after cap should be accepted: len=%d err=%v", len(got), err)
	}
	if _, err := NormalizeTagFilters(append(twenty, "tag-20")); err == nil {
		t.Fatal("twenty-one unique tag filters should be rejected")
	}
}
