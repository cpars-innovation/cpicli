package ops

import (
	"fmt"
	"sync"
)

// Progress is one step of a long operation (a deploy status check, a
// discovery phase, a poll), for progress reporting.
type Progress struct {
	// Step counts the steps so far; it only grows within one operation.
	Step int
	// Total is the expected number of steps, an upper bound (0: unknown).
	Total int
	// Message says what happens, e.g. "artifact X: STARTING, check 3/60".
	Message string
}

// ProgressFunc receives progress. The operations call it from one goroutine
// at a time and before they return; nil reports nothing.
type ProgressFunc func(Progress)

// progress numbers the steps of one operation and serialises the calls to
// fn; the zero value and a nil *progress report nothing.
type progress struct {
	mu   sync.Mutex
	fn   ProgressFunc
	step int
}

func newProgress(fn ProgressFunc) *progress { return &progress{fn: fn} }

// report sends the next step with total (0: unknown).
func (p *progress) report(total int, format string, args ...any) {
	if p == nil || p.fn == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.step++
	p.fn(Progress{Step: p.step, Total: max(total, 0), Message: fmt.Sprintf(format, args...)})
}

// steps returns the number of steps reported so far.
func (p *progress) steps() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.step
}
