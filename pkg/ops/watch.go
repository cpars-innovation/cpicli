package ops

import (
	"context"
	"sort"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// Runtime event types.
const (
	EventDeployed   = "deployed"   // newly on the runtime, or deployed again (new DeployedOn)
	EventStarted    = "started"    // status became STARTED
	EventError      = "error"      // status became ERROR (Error holds the tenant's text)
	EventStatus     = "status"     // any other status change (STARTING, STOPPING, ...)
	EventUndeployed = "undeployed" // gone from the runtime
	EventPresent    = "present"    // initial state (WatchOptions.Initial)
)

// RuntimeEvent is one change on a tenant's runtime.
type RuntimeEvent struct {
	Type string    `json:"type"`
	Time time.Time `json:"time"`
	ID   string    `json:"id"`
	// Runtime is the runtime location for a flow on an integration cell
	// (empty: Cloud Integration).
	Runtime         string     `json:"runtime,omitempty"`
	Version         string     `json:"version,omitempty"`
	PreviousVersion string     `json:"previousVersion,omitempty"`
	Status          string     `json:"status,omitempty"`
	PreviousStatus  string     `json:"previousStatus,omitempty"`
	DeployedOn      *time.Time `json:"deployedOn,omitempty"`
	DeployedBy      string     `json:"deployedBy,omitempty"`
	Error           string     `json:"error,omitempty"`
}

// RuntimeState is one artifact on a runtime, as the watcher compares it.
type RuntimeState struct {
	ID, Runtime, Version, Status, DeployedBy string
	DeployedOn                               time.Time
}

func (s RuntimeState) key() string { return s.Runtime + "\x00" + s.ID }

// DiffRuntime compares two states of the runtime and returns the changes,
// sorted by artifact. A new DeployedOn is a new deployment even with the
// same version and status (a redeploy); a status change after it is
// reported as well.
func DiffRuntime(prev, cur []RuntimeState, at time.Time) []RuntimeEvent {
	before := map[string]RuntimeState{}
	for _, s := range prev {
		before[s.key()] = s
	}
	var out []RuntimeEvent
	seen := map[string]bool{}
	for _, s := range cur {
		seen[s.key()] = true
		old, existed := before[s.key()]
		ev := eventOf(s, at)
		switch {
		case !existed || !s.DeployedOn.Equal(old.DeployedOn) && !s.DeployedOn.IsZero():
			ev.Type = EventDeployed
			if existed {
				ev.PreviousVersion, ev.PreviousStatus = old.Version, old.Status
			}
			out = append(out, ev)
			if s.Status == "ERROR" {
				e := ev
				e.Type = EventError
				out = append(out, e)
			}
		case s.Status != old.Status:
			ev.PreviousStatus, ev.PreviousVersion = old.Status, old.Version
			switch s.Status {
			case "STARTED":
				ev.Type = EventStarted
			case "ERROR":
				ev.Type = EventError
			default:
				ev.Type = EventStatus
			}
			out = append(out, ev)
		case s.Version != old.Version:
			ev.Type, ev.PreviousVersion = EventDeployed, old.Version
			out = append(out, ev)
		}
	}
	for _, s := range prev {
		if !seen[s.key()] {
			ev := eventOf(s, at)
			ev.Type, ev.PreviousStatus, ev.PreviousVersion, ev.Status, ev.Version = EventUndeployed, s.Status, s.Version, "", ""
			out = append(out, ev)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Runtime < out[j].Runtime
	})
	return out
}

func eventOf(s RuntimeState, at time.Time) RuntimeEvent {
	ev := RuntimeEvent{Time: at, ID: s.ID, Runtime: s.Runtime, Version: s.Version, Status: s.Status, DeployedBy: s.DeployedBy}
	if !s.DeployedOn.IsZero() {
		on := s.DeployedOn
		ev.DeployedOn = &on
	}
	return ev
}

// WatchOptions configure WatchRuntime.
type WatchOptions struct {
	// Interval between two reads of the runtime (default 10 s).
	Interval time.Duration
	// Cells also reads the runtimes the public API does not list
	// (integration cells) through the Web UI's runtime list: two more
	// requests per interval and runtime.
	Cells bool
	// Initial reports every artifact found at the start (EventPresent).
	Initial bool
	// Match limits the events to these artifact IDs (nil: all).
	Match func(id string) bool
	// OnError is told about a read that failed; the watch goes on (nil:
	// ignored). Authentication errors end the watch.
	OnError func(error)
}

// WatchRuntime reads the tenant's runtime every interval with one list
// request (plus the Web UI runtime list with Cells) and calls emit for every
// change until ctx ends. An artifact that goes to ERROR gets the tenant's
// error text. It returns ctx's error, or an authentication error.
func WatchRuntime(ctx context.Context, exe *httpclnt.HTTPExecuter, opts WatchOptions, emit func(RuntimeEvent)) error {
	if opts.Interval <= 0 {
		opts.Interval = 10 * time.Second
	}
	rt, content := cpi.NewRuntime(exe), cpi.NewContent(exe)
	read := func() ([]RuntimeState, error) {
		list, err := content.RuntimeArtifacts(nil)
		if err != nil {
			return nil, err
		}
		var out []RuntimeState
		for _, a := range list {
			out = append(out, RuntimeState{ID: a.Id, Version: a.Version, Status: a.Status, DeployedBy: a.DeployedBy, DeployedOn: a.DeployedOn})
		}
		if !opts.Cells {
			return out, nil
		}
		locs, err := rt.Locations()
		if err != nil {
			return nil, err
		}
		for _, l := range locs {
			if l.ID == cpi.CloudIntegrationLocation {
				continue
			}
			arts, err := rt.LocationArtifacts(l.ID)
			if err != nil {
				return nil, err
			}
			for _, a := range arts {
				st := a.Status
				if a.DeployState == "FAILED" && st == "" {
					st = "ERROR"
				}
				out = append(out, RuntimeState{ID: a.Id, Runtime: l.ID, Version: a.Version, Status: st, DeployedBy: a.DeployedBy, DeployedOn: a.DeployedOn})
			}
		}
		return out, nil
	}
	filter := func(list []RuntimeState) []RuntimeState {
		if opts.Match == nil {
			return list
		}
		var out []RuntimeState
		for _, s := range list {
			if opts.Match(s.ID) {
				out = append(out, s)
			}
		}
		return out
	}
	send := func(ev RuntimeEvent) {
		if ev.Type == EventError && ev.Runtime == "" {
			if msg, err := rt.GetErrorInfo(ev.ID); err == nil {
				ev.Error = msg
			}
		}
		emit(ev)
	}

	var prev []RuntimeState
	first := true
	for {
		cur, err := read()
		switch {
		case err != nil && httpclnt.IsAuthError(err):
			return err
		case err != nil:
			if opts.OnError != nil {
				opts.OnError(err)
			}
		default:
			cur = filter(cur)
			if first {
				if opts.Initial {
					for _, s := range cur {
						ev := eventOf(s, time.Now())
						ev.Type = EventPresent
						emit(ev)
					}
				}
				first = false
			} else {
				for _, ev := range DiffRuntime(prev, cur, time.Now()) {
					send(ev)
				}
			}
			prev = cur
		}
		if err := sleep(ctx, opts.Interval); err != nil {
			return err
		}
	}
}
