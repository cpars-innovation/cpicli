package httpclnt

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Boundaries stay unique when batches are built concurrently (run with -race).
func TestGenerateBoundaryConcurrent(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]bool{}
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := generateBoundary("batch_")
			mu.Lock()
			defer mu.Unlock()
			assert.False(t, seen[b], "duplicate boundary %s", b)
			seen[b] = true
		}()
	}
	wg.Wait()
	assert.Len(t, seen, 50)
}
