package httpapi

import "testing"

func TestStreamSlotsRejectsOverCap(t *testing.T) {
	slots := newStreamSlots(1, 1, 1)
	release, err := slots.acquire(1, "1.1.1.1")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if _, err := slots.acquire(2, "1.1.1.2"); err == nil {
		t.Fatal("global cap should reject")
	}
	release()
	release, err = slots.acquire(3, "1.1.1.1")
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	if _, err := slots.acquire(3, "2.2.2.2"); err == nil {
		t.Fatal("per-board cap should reject")
	}
	release()
}

func TestStreamSlotsPerIP(t *testing.T) {
	slots := newStreamSlots(10, 1, 10)
	release, err := slots.acquire(1, "9.9.9.9")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	defer release()
	if _, err := slots.acquire(2, "9.9.9.9"); err == nil {
		t.Fatal("per-IP cap should reject")
	}
}
