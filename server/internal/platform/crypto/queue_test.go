package crypto

import (
	"context"
	"errors"
	"testing"
	"time"
)

// With every worker busy a caller waits only as long as its context allows,
// and once the queue is full further callers are refused at once.
func TestPasswordQueueIsBoundedAndCancellable(t *testing.T) {
	for range hashWorkers {
		hashSlots <- struct{}{}
	}
	defer func() {
		for range hashWorkers {
			<-hashSlots
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := DummyVerifyContext(ctx, "pw")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting caller: err = %v, want the context's deadline", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("a cancelled caller waited %v", time.Since(start))
	}
	if len(hashQueue) != 0 {
		t.Fatalf("queue holds %d entries after the caller left", len(hashQueue))
	}

	for range hashMaxQueue {
		hashQueue <- struct{}{}
	}
	defer func() {
		for range hashMaxQueue {
			<-hashQueue
		}
	}()
	start = time.Now()
	if _, err := HashPasswordContext(context.Background(), "pw"); !errors.Is(err, ErrBusy) {
		t.Fatalf("full queue: err = %v, want ErrBusy", err)
	}
	if ok, err := VerifyPasswordContext(context.Background(), "pw", "$argon2id$v=19$m=65536,t=3,p=4$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5"); ok || !errors.Is(err, ErrBusy) {
		t.Fatalf("full queue verify: %v, %v", ok, err)
	}
	if time.Since(start) > time.Second {
		t.Errorf("a refused caller waited %v", time.Since(start))
	}
}
