package ratelimit

import (
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestBurstThenRefill(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	l := NewWithClock(3, 10*time.Second, c.now)
	for i := range 3 {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("burst event %d refused", i)
		}
	}
	ok, wait := l.Allow("a")
	if ok {
		t.Fatal("fourth event allowed")
	}
	if wait < 9*time.Second || wait > 10*time.Second {
		t.Errorf("retry after = %v, want about 10s", wait)
	}
	c.advance(10 * time.Second)
	if ok, _ := l.Allow("a"); !ok {
		t.Error("not refilled after one interval")
	}
	if ok, _ := l.Allow("a"); ok {
		t.Error("refilled more than one token")
	}
}

func TestKeysAreIndependent(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	l := NewWithClock(1, time.Minute, c.now)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("first a refused")
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("second a allowed")
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("b affected by a")
	}
}

func TestSweepDropsIdleKeys(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	l := NewWithClock(2, time.Second, c.now)
	for _, k := range []string{"a", "b", "c"} {
		l.Allow(k)
	}
	c.advance(5 * time.Minute)
	l.Allow("d")
	if n := l.Len(); n != 1 {
		t.Errorf("tracked keys = %d, want 1 after sweep", n)
	}
}

func TestDisabledAndNil(t *testing.T) {
	var nilL *Limiter
	if ok, _ := nilL.Allow("x"); !ok {
		t.Error("nil limiter refused")
	}
	if ok, _ := New(0, time.Second).Allow("x"); !ok {
		t.Error("zero-burst limiter refused")
	}
}
