package cpitest

import (
	"cmp"
	"context"
	"fmt"
	mrand "math/rand/v2"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// newExecutor returns an executor with the tenant's systems and random
// source; guids are prefixed (AG<prefix><serial>).
func (m *Tenant) newExecutor(prefix string) *executor {
	if m.rng == nil {
		m.rng = mrand.New(mrand.NewPCG(1, 1))
	}
	return &executor{m: m, rng: m.rng, systems: m.systems, guid: func() string {
		m.msgSerial++
		return fmt.Sprintf("AG%s%0*d", prefix, 26-len(prefix), m.msgSerial)
	}}
}

// keyHeaders are the landscape's business key headers.
func (m *Tenant) keyHeaders() []string {
	if m.landscape == nil {
		return nil
	}
	return m.landscape.KeyHeaders
}

func (r TrafficRule) key(n int64) string {
	if r.Key == "" {
		return ""
	}
	return strings.ReplaceAll(r.Key, "{n}", fmt.Sprint(n))
}

func (m *Tenant) payload(key string) string {
	if key == "" {
		return "<Message/>"
	}
	tag := "Key"
	if h := m.keyHeaders(); len(h) > 0 {
		tag = h[0]
	}
	return fmt.Sprintf("<Message><%s>%s</%s></Message>", tag, key, tag)
}

func (r TrafficRule) onTier(tier string) bool {
	return len(r.Tiers) == 0 || slices.Contains(r.Tiers, tier)
}

func (f FollowUp) onTier(tier string) bool {
	return len(f.Tiers) == 0 || slices.Contains(f.Tiers, tier)
}

// history generates the message logs of the landscape's traffic over the
// history window before now, newest first. Runs that would end after now
// are left out.
func (m *Tenant) history(now time.Time) []MessageLog {
	l, t := m.landscape, m.tierSpec
	m.rng = mrand.New(mrand.NewPCG(l.seed(t.Name), 7))
	m.systems = l.systems(t)
	window := time.Duration(l.History)
	if window <= 0 {
		window = 24 * time.Hour
	}
	scale := t.TrafficScale
	if scale <= 0 {
		scale = 1
	}
	x := m.newExecutor(strings.ToUpper(t.Name[:1]))
	for ri, r := range l.Traffic {
		a := m.Artifacts[r.Start]
		if !r.onTier(t.Name) || a == nil || !a.running() || a.info() == nil {
			continue
		}
		n := int(window.Hours() * r.PerHour * scale)
		if n <= 0 {
			continue
		}
		interval := window / time.Duration(n)
		counter := max(r.KeyStart, 1)
		for i := range n {
			at := now.Add(-window).Add(time.Duration(i) * interval).Add(time.Duration(x.rng.Int64N(int64(interval)/3 + 1)))
			key := r.key(counter)
			counter++
			corr := fmt.Sprintf("C-%s-%d-%d", t.Name, ri, i+1)
			x.run(r.Start, message{correlation: corr, key: key, payload: m.payload(key), keyHeaders: l.KeyHeaders}, at, "", 0)
			for fi, f := range r.FollowUps {
				every := max(f.Every, 1)
				if !f.onTier(t.Name) || (i+1)%every != 0 || m.Artifacts[f.Start] == nil || !m.Artifacts[f.Start].running() {
					continue
				}
				x.run(f.Start, message{correlation: fmt.Sprintf("%s-F%d", corr, fi+1), key: key, payload: m.payload(key), keyHeaders: l.KeyHeaders},
					at.Add(time.Duration(f.After)), "", 0)
			}
		}
	}
	logs := slices.DeleteFunc(x.logs, func(l MessageLog) bool { return l.End.After(now) })
	sort.SliceStable(logs, func(i, j int) bool { return logs[i].Start.After(logs[j].Start) })
	return logs
}

const (
	defaultLogRetention = 168 * time.Hour
	defaultMaxLogs      = 100_000
)

// addLogs adds message logs to the live log list, which stays newest first.
// The batch (small, from one run) is sorted and merged into the front part
// of the list it overlaps with: new logs are usually the newest, so the
// full list is never re-sorted. Logs older than the retention behind the
// newest log, or beyond the cap, are dropped. The age is measured from the
// newest log, not the clock, so that a mock seeded in the past and logs
// added for an earlier period stay. It returns how many of logs were kept.
func (m *Tenant) addLogs(logs []MessageLog) (kept int) {
	if len(m.MessageLogSteps) == 0 {
		m.MessageLogSteps = [][]MessageLog{nil}
	}
	last := len(m.MessageLogSteps) - 1
	all := m.MessageLogSteps[last]
	if len(logs) > 0 {
		batch := slices.Clone(logs)
		sort.SliceStable(batch, func(i, j int) bool { return batch[i].Start.After(batch[j].Start) })
		// all[:p] are not older than the oldest new log: merge those.
		oldest := batch[len(batch)-1].Start
		p := sort.Search(len(all), func(i int) bool { return all[i].Start.Before(oldest) })
		merged := make([]MessageLog, 0, p+len(batch))
		i, j := 0, 0
		for i < p || j < len(batch) {
			if j == len(batch) || (i < p && !batch[j].Start.After(all[i].Start)) {
				merged = append(merged, all[i])
				i++
			} else {
				merged = append(merged, batch[j])
				j++
			}
		}
		all = slices.Insert(all[p:], 0, merged...)
	}
	if retention := cmp.Or(m.LogRetention, defaultLogRetention); retention > 0 && len(all) > 0 {
		cutoff := all[0].Start.Add(-retention)
		n := sort.Search(len(all), func(i int) bool { return all[i].Start.Before(cutoff) })
		clear(all[n:])
		all = all[:n]
	}
	if limit := cmp.Or(m.MaxLogs, defaultMaxLogs); len(all) > limit {
		clear(all[limit:])
		all = all[:limit]
	}
	m.MessageLogSteps[last] = all
	added := make(map[string]int, len(logs))
	for _, l := range logs {
		added[l.Guid]++
	}
	for _, l := range all {
		if added[l.Guid] > 0 {
			added[l.Guid]--
			kept++
		}
	}
	return kept
}

// messageKey finds the business key of a received message: the
// SAP_ApplicationID header, a key header sent as HTTP header, or a key
// header element in the payload.
func (m *Tenant) messageKey(r *http.Request, body string) string {
	if v := r.Header.Get("SAP_ApplicationID"); v != "" {
		return v
	}
	for _, h := range m.keyHeaders() {
		if v := r.Header.Get(h); v != "" {
			return v
		}
		re := regexp.MustCompile(`<` + regexp.QuoteMeta(h) + `>([^<]+)</` + regexp.QuoteMeta(h) + `>`)
		if mm := re.FindStringSubmatch(body); mm != nil {
			return mm[1]
		}
	}
	return ""
}

// StartTraffic generates the landscape's traffic live, in real time, until
// ctx ends (cpictl mock-tenant --live-traffic). speed > 1 makes it faster.
func (m *Tenant) StartTraffic(ctx context.Context, speed float64) {
	m.mu.Lock()
	l, t := m.landscape, m.tierSpec
	m.mu.Unlock()
	if l == nil || !(speed > 0) { // also NaN
		return
	}
	scale := t.TrafficScale
	if scale <= 0 {
		scale = 1
	}
	for ri, r := range l.Traffic {
		if !r.onTier(t.Name) {
			continue
		}
		// A huge rate must not make the interval zero (NewTicker panics).
		interval := max(time.Duration(float64(time.Hour)/(r.PerHour*scale*speed)), time.Millisecond)
		go func(ri int, r TrafficRule) {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			counter := max(r.KeyStart, 1) + 1_000_000
			for i := 1; ; i++ {
				select {
				case <-ctx.Done():
					return
				case now := <-ticker.C:
					m.mu.Lock()
					if a := m.Artifacts[r.Start]; a != nil && a.running() && a.info() != nil {
						x := m.newExecutor("LIVE")
						key := r.key(counter)
						corr := fmt.Sprintf("C-%s-live-%d-%d", t.Name, ri, i)
						x.run(r.Start, message{correlation: corr, key: key, payload: m.payload(key), keyHeaders: l.KeyHeaders}, now, "", 0)
						m.addLogs(x.logs)
					}
					counter++
					m.mu.Unlock()
				}
			}
		}(ri, r)
	}
}

// inboundRun handles a message received by a runtime endpoint in Live mode:
// it runs the flow behind the endpoint (when it is deployed and the mock can
// read it) and adds its logs, or logs a COMPLETED message for an endpoint
// without a runnable flow. It sets the message headers on w. ran reports
// that the flow ran, failed its error text when the run failed. Without Live
// only the endpoint's MessageGuid is set. The ApplicationMessageId is the
// SAP_ApplicationID header when sent, else what the flow sets.
func (m *Tenant) inboundRun(w http.ResponseWriter, r *http.Request, in *Inbound, msg message) (ran bool, failed string) {
	appID := r.Header.Get("SAP_ApplicationID")
	a := m.Artifacts[in.Artifact]
	switch {
	case m.Live && a != nil && a.running() && a.info() != nil:
		m.liveSerial++
		msg.correlation = fmt.Sprintf("C-live-%d", m.liveSerial)
		msg.keyHeaders = m.keyHeaders()
		x := m.newExecutor("LIVE")
		res := x.run(in.Artifact, msg, time.Now(), "", 0)
		if appID != "" {
			for i := range x.logs {
				if x.logs[i].Guid == res.guid {
					x.logs[i].ApplicationID = appID
				}
			}
		}
		m.addLogs(x.logs)
		w.Header().Set("SAP_MessageProcessingLogID", res.guid)
		w.Header().Set("SAP_MplCorrelationId", msg.correlation)
		if res.status != "COMPLETED" {
			return true, res.errorText
		}
		return true, ""
	case m.Live && in.Artifact != "":
		m.liveSerial++
		now := time.Now()
		l := MessageLog{Guid: fmt.Sprintf("AGLIVE%022d", m.liveSerial), Artifact: in.Artifact, Status: "COMPLETED",
			CorrelationID: fmt.Sprintf("C-live-%d", m.liveSerial), ApplicationID: cmp.Or(appID, msg.docnum),
			Start: now, End: now.Add(150 * time.Millisecond)}
		if a != nil {
			l.Package = a.Package
		}
		m.addLogs([]MessageLog{l})
		w.Header().Set("SAP_MessageProcessingLogID", l.Guid)
		w.Header().Set("SAP_MplCorrelationId", l.CorrelationID)
	case in.MessageGuid != "":
		w.Header().Set("SAP_MessageProcessingLogID", in.MessageGuid)
	}
	return false, ""
}

// MessageLogs returns a copy of the live message logs (newest first).
func (m *Tenant) MessageLogs() []MessageLog {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.MessageLogSteps) == 0 {
		return nil
	}
	return slices.Clone(m.MessageLogSteps[len(m.MessageLogSteps)-1])
}
