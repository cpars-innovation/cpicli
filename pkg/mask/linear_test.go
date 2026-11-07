package mask

import (
	"testing"
	"time"
)

// assertLinear checks that f grows linearly: f on 4n units may take at most
// ten times f on n units (quadratic growth is sixteen). It compares the
// two instead of an absolute limit, so a loaded machine does not fail it
// (cpicli#78); times under the floor count as noise.
func assertLinear(t *testing.T, name string, n int, build func(n int) string, f func(string)) {
	t.Helper()
	measure := func(s string) time.Duration {
		best := time.Duration(1<<63 - 1)
		for range 2 {
			start := time.Now()
			f(s)
			best = min(best, time.Since(start))
		}
		return best
	}
	small, large := measure(build(n)), measure(build(4*n))
	if large > 10*small && large > 50*time.Millisecond*raceSlowdown {
		t.Errorf("%s: %d units %v, %d units %v: not linear", name, n, small, 4*n, large)
	}
}
