package output

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"time"
)

// Event is one line of the progress event stream (--events): a JSON object
// per line, written while a command runs. The result document (--output
// json) stays the final word; events are for progress displays.
type Event struct {
	SchemaVersion int       `json:"schemaVersion"`
	Seq           int       `json:"seq"`
	Time          time.Time `json:"time"`
	Command       string    `json:"command"`
	// Event: start, item (an artifact or package was handled), end.
	Event string `json:"event"`
	// Data of an item event is the command's item (snapshot item,
	// orchestrator artifact, deploy result); of the end event the exit code.
	Data any `json:"data,omitempty"`
}

// Event names.
const (
	EventStart = "start"
	EventItem  = "item"
	EventEnd   = "end"
)

// EventEnd data.
type EndData struct {
	ExitCode      int    `json:"exitCode"`
	ErrorCategory string `json:"errorCategory"`
	DurationMs    int64  `json:"durationMs"`
}

type eventsKey struct{}

type eventSink struct {
	mu      sync.Mutex
	w       io.WriteCloser
	command string
	seq     int
}

// WithEvents adds an (inactive) event sink to ctx; StartEvents activates it.
func WithEvents(ctx context.Context) context.Context {
	return context.WithValue(ctx, eventsKey{}, &eventSink{})
}

// StartEvents writes the events of command to w from now on and emits the
// start event; CloseEvents closes w.
func StartEvents(ctx context.Context, w io.WriteCloser, command string) {
	s, _ := ctx.Value(eventsKey{}).(*eventSink)
	if s == nil || w == nil {
		return
	}
	s.mu.Lock()
	s.w, s.command = w, command
	s.mu.Unlock()
	Emit(ctx, EventStart, nil)
}

// EventsActive tells whether events are written (to skip building data).
func EventsActive(ctx context.Context) bool {
	s, _ := ctx.Value(eventsKey{}).(*eventSink)
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w != nil
}

// Emit writes one event; without an active sink it does nothing. Safe for
// concurrent use. Write errors are ignored: events never fail a command.
func Emit(ctx context.Context, event string, data any) {
	s, _ := ctx.Value(eventsKey{}).(*eventSink)
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.w == nil {
		return
	}
	s.seq++
	line, err := json.Marshal(Event{SchemaVersion: SchemaVersion, Seq: s.seq, Time: time.Now().UTC(), Command: s.command, Event: event, Data: data})
	if err != nil {
		return
	}
	_, _ = s.w.Write(append(line, '\n'))
}

// CloseEvents ends the event stream and closes its writer.
func CloseEvents(ctx context.Context) {
	s, _ := ctx.Value(eventsKey{}).(*eventSink)
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.w != nil {
		_ = s.w.Close()
		s.w = nil
	}
}
