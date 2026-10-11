package ops

import (
	"sync"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/rs/zerolog/log"
)

// Tracing exposes payloads, so a raised log level should be time-boxed:
// LogLevelReverter sets flows back to INFO once their deadline has passed
// (RunDue, e.g. before every request) and when the program ends (RevertAll).

// LogLevelReverter remembers pending log level reverts. It is safe for
// concurrent use.
type LogLevelReverter struct {
	// Now is the clock (default time.Now).
	Now     func() time.Time
	mu      sync.Mutex
	exe     *httpclnt.HTTPExecuter
	pending map[string]pendingRevert
}

type pendingRevert struct {
	at  time.Time
	req LogLevelRequest
}

// NewLogLevelReverter returns a reverter that uses exe.
func NewLogLevelReverter(exe *httpclnt.HTTPExecuter) *LogLevelReverter {
	return &LogLevelReverter{exe: exe, Now: time.Now, pending: map[string]pendingRevert{}}
}

// Schedule reverts req.ArtifactID to INFO at at (replacing an earlier one
// of the flow; req.Level is ignored); a zero at cancels a pending revert.
func (r *LogLevelReverter) Schedule(req LogLevelRequest, at time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if at.IsZero() {
		delete(r.pending, req.ArtifactID)
		return
	}
	req.Level = "INFO"
	r.pending[req.ArtifactID] = pendingRevert{at: at, req: req}
}

// Pending returns the pending reverts: artifact ID -> deadline.
func (r *LogLevelReverter) Pending() map[string]time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]time.Time, len(r.pending))
	for id, p := range r.pending {
		out[id] = p.at
	}
	return out
}

// RunDue reverts every flow whose deadline has passed.
func (r *LogLevelReverter) RunDue() { r.run(false) }

// RevertAll reverts every pending flow now (shutdown).
func (r *LogLevelReverter) RevertAll() { r.run(true) }

func (r *LogLevelReverter) run(all bool) {
	r.mu.Lock()
	var due []LogLevelRequest
	now := r.Now()
	for id, p := range r.pending {
		if all || !now.Before(p.at) {
			due = append(due, p.req)
			delete(r.pending, id)
		}
	}
	r.mu.Unlock()
	for _, req := range due {
		if _, err := SetLogLevel(r.exe, req); err != nil {
			log.Warn().Str("artifact", req.ArtifactID).Msgf("Could not set the log level back to INFO: %v", err)
		} else {
			log.Info().Str("artifact", req.ArtifactID).Msg("Log level set back to INFO")
		}
	}
}
