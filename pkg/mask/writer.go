package mask

import (
	"bytes"
	"io"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
)

// Writer masks text written through it line by line, like InPlace (a
// private key block across lines too), before it reaches W. It is off until
// Enable; while off it passes everything through unchanged. Close (or
// Flush) writes a last line without a newline. It is safe for concurrent
// use; lines are written whole.
type Writer struct {
	W      io.Writer
	Masker *Masker

	on    atomic.Bool
	mu    sync.Mutex
	buf   []byte
	inKey bool
	stats Stats
}

// NewWriter returns a Writer that is off.
func NewWriter(w io.Writer, m *Masker) *Writer { return &Writer{W: w, Masker: m} }

// Enable turns masking on (or off).
func (w *Writer) Enable(on bool) { w.on.Store(on) }

// Enabled reports whether masking is on.
func (w *Writer) Enabled() bool { return w.on.Load() }

// Write masks every complete line of p; the rest waits for its newline.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.on.Load() && len(w.buf) == 0 {
		return w.W.Write(p)
	}
	w.buf = append(w.buf, p...)
	i := bytes.LastIndexByte(w.buf, '\n')
	if i < 0 {
		return len(p), nil
	}
	out := w.mask(string(w.buf[:i+1]))
	w.buf = append(w.buf[:0], w.buf[i+1:]...)
	if _, err := io.WriteString(w.W, out); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Flush writes what waits for a newline.
func (w *Writer) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) == 0 {
		return nil
	}
	out := w.mask(string(w.buf))
	w.buf = w.buf[:0]
	_, err := io.WriteString(w.W, out)
	return err
}

// Close flushes.
func (w *Writer) Close() error { return w.Flush() }

// Stats returns how many values were masked so far, per detector.
func (w *Writer) Stats() Stats {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := Stats{}
	out.Add(w.stats)
	return out
}

func (w *Writer) mask(s string) string {
	if !w.on.Load() {
		return s
	}
	if w.stats == nil {
		w.stats = Stats{}
	}
	lines := strings.SplitAfter(s, "\n")
	for i, l := range lines {
		body := strings.TrimRight(l, "\r\n")
		end := l[len(body):]
		switch {
		case w.inKey:
			if rePEMEnd.MatchString(body) {
				w.inKey = false
			} else if strings.TrimSpace(body) != "" {
				w.stats[DetectPrivateKey]++
				body = leadingSpace(body) + Dots
			}
		case rePEMBegin.MatchString(body) && !rePEMEnd.MatchString(body) && w.Masker.has(DetectPrivateKey):
			w.inKey = true
		default:
			body = w.line(body)
		}
		lines[i] = body + end
	}
	return strings.Join(lines, "")
}

// reANSI is a terminal escape sequence (colours in console logs).
var reANSI = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// line masks the text of a line between its escape sequences, so that a
// detector neither takes a sequence's characters nor breaks it.
func (w *Writer) line(s string) string {
	if !strings.Contains(s, "\x1b[") {
		return w.Masker.line(s, w.stats)
	}
	var b strings.Builder
	last := 0
	for _, loc := range reANSI.FindAllStringIndex(s, -1) {
		b.WriteString(w.Masker.line(s[last:loc[0]], w.stats))
		b.WriteString(s[loc[0]:loc[1]])
		last = loc[1]
	}
	b.WriteString(w.Masker.line(s[last:], w.stats))
	return b.String()
}
