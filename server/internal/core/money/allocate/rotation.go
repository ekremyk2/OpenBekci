package allocate

import (
	"hash/fnv"
	"strconv"
)

// Option tunes an allocation.
type Option func(*config)

type config struct {
	rotation string
	rotate   bool
}

// WithRotation rotates the leftover-minor-unit tie-break by a period key
// (for example "2026-03" or the charge's period key plus its charge-type
// code). Without it ties always go to the first units in order, so with 7 units
// and 100 kuruş the same two pay 15 every month. With it, the start of the tie
// order is offset by fnv1a64(key) mod n over the ordered units, which is
// deterministic (same key and units, same result), spreads the extra kuruş
// across units over successive periods, and never changes the sum. Only
// which of several equal-remainder units gets the leftover changes. A
// composite key rotates each part by key + "/" + part index.
func WithRotation(key string) Option {
	return func(c *config) { c.rotation, c.rotate = key, true }
}

func newConfig(opts []Option) config {
	var c config
	for _, o := range opts {
		o(&c)
	}
	return c
}

// rank returns the tie-break rank of each of n ordered units: identity, or
// rotated by the hash of the rotation key.
func (c config) rank(n int) []int {
	rank := make([]int, n)
	off := 0
	if c.rotate && n > 0 {
		h := fnv.New64a()
		_, _ = h.Write([]byte(c.rotation))
		off = int(h.Sum64() % uint64(n))
	}
	for i := range rank {
		rank[i] = (i - off + n) % n
	}
	return rank
}

// part is the option set for composite part i.
func (c config) part(i int) []Option {
	if !c.rotate {
		return nil
	}
	return []Option{WithRotation(c.rotation + "/" + strconv.Itoa(i))}
}
