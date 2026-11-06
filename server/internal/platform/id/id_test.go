package id

import "testing"

func TestNewIsV7AndOrdered(t *testing.T) {
	a, b := New(), New()
	if a.Version() != 7 {
		t.Fatalf("version = %d, want 7", a.Version())
	}
	if a == b {
		t.Fatal("two ids are equal")
	}
	if a.String() >= b.String() {
		t.Fatalf("ids not ordered: %s >= %s", a, b)
	}
}
