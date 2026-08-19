package httpapi

import (
	"encoding/json"
	"testing"

	"scrumboy/internal/store"
)

func TestTodoJSONCreatorAttributionOmissionAndValue(t *testing.T) {
	t.Run("nil creator is omitted", func(t *testing.T) {
		encoded, err := json.Marshal(todoToJSON(store.Todo{ID: 1, LocalID: 1}))
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(encoded, &payload); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if _, exists := payload["createdByUserId"]; exists {
			t.Fatalf("nil createdByUserId must be omitted: %s", encoded)
		}
	})

	t.Run("known creator is projected", func(t *testing.T) {
		creatorID := int64(42)
		encoded, err := json.Marshal(todoToJSON(store.Todo{ID: 1, LocalID: 1, CreatedByUserID: &creatorID}))
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(encoded, &payload); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if payload["createdByUserId"] != float64(creatorID) {
			t.Fatalf("createdByUserId=%v, want %d", payload["createdByUserId"], creatorID)
		}
	})
}
