package cpitest

import (
	"cmp"
	"fmt"
	mrand "math/rand/v2"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/iflow"
	"github.com/cpars-innovation/cpicli/pkg/testtools"
)

// The mock executes flows the way the message processing logs show them: one
// run per flow with its steps; ProcessDirect calls the next flow
// synchronously (same correlation ID, predecessor link, a failure propagates
// to the caller), JMS hands the message to the consuming flow a moment later
// (same correlation ID; a failure there is RETRY), and the other receivers
// are systems from the landscape (latency, failure rate, error text). No
// script or mapping is executed: payloads pass through unchanged.

// message is one business message travelling through the tenant.
type message struct {
	correlation string
	key         string
	payload     string
	keyHeaders  []string
	// docnum is the IDoc number (DOCNUM) of a message an IDoc sender received.
	docnum string
	// headers are the message headers (the HTTP headers a sender received);
	// dynamic addresses ${header.<name>} resolve against them.
	headers map[string]string
}

// runResult is the outcome of one flow run.
type runResult struct {
	guid, status, errorText string
	end                     time.Time
}

// executor runs flows; it collects the logs it writes.
type executor struct {
	m       *Tenant
	rng     *mrand.Rand
	systems map[string]SystemSpec
	guid    func() string
	logs    []MessageLog
}

// flowInfo is the cached executable view of an artifact.
type flowInfo struct {
	fm            *flowModel
	steps         []*iflow.Element
	customHeaders map[string]bool // custom header properties the scripts write
	setsAppID     bool            // scripts set SAP_ApplicationID
	jmsConsumer   bool
	idocSender    bool
	// attachments are the attachment names the script of a step writes to
	// the message processing log, by step ID.
	attachments map[string][]string
	// stub: a script records requests as testtools.StubAttachment (the
	// receiver stub, emulated by runStub).
	stub bool
}

var (
	reCustomHeaderProp = regexp.MustCompile(`addCustomHeaderProperty\s*\(\s*["']([^"']+)["']`)
	reAttachment       = regexp.MustCompile(`addAttachmentAs(?:String|Binary)\s*\(\s*["']([^"']+)["']`)
)

func (a *Artifact) info() *flowInfo {
	if a.cached != nil {
		return a.cached
	}
	fm := a.flow()
	if fm == nil {
		return nil
	}
	fi := &flowInfo{fm: fm, customHeaders: map[string]bool{}}
	for _, r := range a.Resources {
		if r.Type != "groovy" && r.Type != "gsh" && r.Type != "js" {
			continue
		}
		for _, mm := range reCustomHeaderProp.FindAllStringSubmatch(string(r.Content), -1) {
			fi.customHeaders[mm[1]] = true
		}
		if strings.Contains(string(r.Content), "SAP_ApplicationID") {
			fi.setsAppID = true
		}
		if strings.Contains(string(r.Content), testtools.StubAttachment) {
			fi.stub = true
		}
	}
	for _, s := range fm.Senders {
		switch s.Adapter {
		case "JMS":
			fi.jmsConsumer = true
		case adapterIDoc:
			fi.idocSender = true
		}
	}
	fi.steps = mainPath(fm.model)
	for _, el := range fi.steps {
		if script := el.Props["script"]; script != "" {
			for _, mm := range reAttachment.FindAllStringSubmatch(string(a.Resources[path.Base(script)].Content), -1) {
				if fi.attachments == nil {
					fi.attachments = map[string][]string{}
				}
				fi.attachments[el.ID] = append(fi.attachments[el.ID], mm[1])
			}
		}
	}
	a.cached = fi
	return fi
}

// mainPath returns the steps of the integration process in execution order:
// from the start event along the single outgoing flows (the first branch at
// a gateway). Like SAP's runtime it follows only the outgoing references an
// element declares: without them the process runs no step.
func mainPath(m *iflow.Model) []*iflow.Element {
	undeclared := map[string]bool{}
	for _, r := range m.MissingRefs {
		if !r.Incoming {
			undeclared[r.Element+"/"+r.Flow] = true
		}
	}
	var start *iflow.Element
	for _, id := range m.Order {
		el := m.Elements[id]
		if el.Tag == "startEvent" && !inEventSubprocess(m, el) {
			start = el
			break
		}
	}
	var out []*iflow.Element
	seen := map[string]bool{}
	for el := start; el != nil && !seen[el.ID]; {
		seen[el.ID] = true
		if el.IsStep() {
			out = append(out, el)
		}
		if len(el.Outgoing) == 0 || undeclared[el.ID+"/"+el.Outgoing[0]] {
			break
		}
		f := m.Flow(el.Outgoing[0])
		if f == nil {
			break
		}
		el = m.Elements[f.Target]
	}
	return out
}

func inEventSubprocess(m *iflow.Model, el *iflow.Element) bool {
	c := m.Elements[el.Container]
	return c != nil && c.TriggeredByEvent
}

// flowBySender finds the deployed flow whose sender adapter listens on address.
func (x *executor) flowBySender(adapter, address string) (string, *Artifact) {
	for _, id := range sortedKeys(x.m.Artifacts) {
		a := x.m.Artifacts[id]
		if !a.running() {
			continue
		}
		fi := a.info()
		if fi == nil {
			continue
		}
		for _, s := range fi.fm.Senders {
			if s.Adapter == adapter && resolve(s.Address, a.Parameters) == address {
				return id, a
			}
		}
	}
	return "", nil
}

// noConsumer is the error of a ProcessDirect call nobody consumes; the mock
// adds the addresses of the running ProcessDirect senders (a tenant does
// not) so that a mismatch or an undeployed flow is easy to see.
func (x *executor) noConsumer(address string) string {
	var running []string
	for _, id := range sortedKeys(x.m.Artifacts) {
		a := x.m.Artifacts[id]
		if fi := a.info(); a.running() && fi != nil {
			for _, s := range fi.fm.Senders {
				if s.Adapter == "ProcessDirect" {
					running = append(running, resolve(s.Address, a.Parameters)+" ("+id+")")
				}
			}
		}
	}
	text := fmt.Sprintf("com.sap.it.rt.adapter.processdirect.ProcessDirectException: no consumer for address %s", address)
	if len(running) == 0 {
		return text + " [mock: no deployed flow has a ProcessDirect sender]"
	}
	return text + " [mock: deployed ProcessDirect senders: " + strings.Join(running, ", ") + "]"
}

// run executes one run of flow id for msg starting at start.
func (x *executor) run(id string, msg message, start time.Time, predecessor string, depth int) runResult {
	a := x.m.Artifacts[id]
	fi := a.info()
	l := MessageLog{Guid: x.guid(), Artifact: id, Package: a.Package, Status: "COMPLETED", CorrelationID: msg.correlation,
		Predecessor: predecessor, Start: start}
	if fi.setsAppID && msg.key != "" {
		l.ApplicationID = msg.key
	}
	if l.ApplicationID == "" && fi.idocSender { // the IDoc number, unless the flow sets one
		l.ApplicationID = cmp.Or(msg.docnum, docnumFor(msg.key))
	}
	if msg.key != "" {
		for _, h := range msg.keyHeaders {
			if fi.customHeaders[h] {
				if l.Headers == nil {
					l.Headers = map[string]string{}
				}
				l.Headers[h] = msg.key
			}
		}
	}
	t := start.Add(time.Duration(5+x.rng.IntN(20)) * time.Millisecond)
	var failedAt *iflow.Element
	fail := func(el *iflow.Element, text string) {
		failedAt, l.ErrorText = el, text
	}
	called := map[string]bool{}
	for i, el := range fi.steps {
		step := Step{StepID: fmt.Sprintf("%s-%d", l.Guid, i+1), ModelStepID: el.ID, Activity: stepName(el), Status: "COMPLETED"}
		t = t.Add(time.Duration(10+x.rng.IntN(60)) * time.Millisecond)
		for _, ch := range fi.fm.Receivers {
			if ch.Source != el.ID || failedAt != nil {
				continue
			}
			called[ch.Source+ch.Address] = true
			var errText string
			t, errText = x.call(ch, a, msg, t, l.Guid, depth)
			if errText != "" {
				fail(el, errText)
			}
		}
		for _, name := range fi.attachments[el.ID] { // the script logs the message
			if l.Attachments == nil {
				l.Attachments = map[string]string{}
			}
			l.Attachments[name] = msg.payload
		}
		if failedAt == el {
			step.Status, step.Error = "FAILED", l.ErrorText
			step.Traces = []Trace{x.trace(msg, l)}
		}
		l.Steps = append(l.Steps, step)
		if failedAt != nil {
			break
		}
	}
	// receivers at the end of the process: the message leaves the flow
	if failedAt == nil {
		for _, ch := range fi.fm.Receivers {
			if called[ch.Source+ch.Address] {
				continue
			}
			var errText string
			t, errText = x.call(ch, a, msg, t, l.Guid, depth)
			if errText != "" {
				end := &iflow.Element{ID: ch.Source, Name: "End"}
				fail(end, errText)
				l.Steps = append(l.Steps, Step{StepID: fmt.Sprintf("%s-%d", l.Guid, len(l.Steps)+1), ModelStepID: ch.Source,
					Activity: "Send to " + ch.Adapter, Status: "FAILED", Error: errText, Traces: []Trace{x.trace(msg, l)}})
				break
			}
		}
	}
	if failedAt != nil {
		l.Status = "FAILED"
		if fi.jmsConsumer {
			l.Status = "RETRY"
			for _, snd := range fi.fm.Senders {
				if snd.Adapter == "JMS" && snd.Address != "" {
					l.End = t
					x.m.addJMSRetry(resolve(snd.Address, a.Parameters), l)
				}
			}
		}
	}
	l.End = t
	x.logs = append(x.logs, l)
	return runResult{guid: l.Guid, status: l.Status, errorText: l.ErrorText, end: t}
}

// docnumFor is the IDoc number of a generated message with key: a numeric
// key padded to DOCNUM's 16 digits, other keys as they are.
func docnumFor(key string) string {
	if key == "" || len(key) >= 16 || strings.Trim(key, "0123456789") != "" {
		return key
	}
	return strings.Repeat("0", 16-len(key)) + key
}

func stepName(el *iflow.Element) string {
	if el.Name != "" {
		return el.Name
	}
	return el.Kind()
}

func (x *executor) trace(msg message, l MessageLog) Trace {
	x.m.traceSerial++
	h := map[string]string{"SAP_MessageProcessingLogID": l.Guid, "SAP_MplCorrelationId": l.CorrelationID}
	if msg.key != "" {
		h["SAP_ApplicationID"] = msg.key
	}
	return Trace{ID: fmt.Sprint(1000 + x.m.traceSerial), Payload: msg.payload, Headers: h,
		ExchangeProperties: map[string]string{"CamelExceptionCaught": l.ErrorText}}
}

// call sends the message through one receiver channel; it returns the time
// after the call and an error text when it failed.
func (x *executor) call(ch channel, a *Artifact, msg message, t time.Time, guid string, depth int) (time.Time, string) {
	address := resolveHeaders(resolve(ch.Address, a.Parameters), msg.headers)
	switch ch.Adapter {
	case "ProcessDirect":
		id, _ := x.flowBySender("ProcessDirect", address)
		if id == "" || depth > 20 {
			return t.Add(5 * time.Millisecond), x.noConsumer(address)
		}
		r := x.run(id, msg, t.Add(3*time.Millisecond), guid, depth+1)
		if r.status != "COMPLETED" {
			return r.end, r.errorText
		}
		return r.end.Add(2 * time.Millisecond), ""
	case "JMS":
		if id, _ := x.flowBySender("JMS", address); id != "" && depth <= 20 {
			x.run(id, msg, t.Add(time.Duration(800+x.rng.IntN(1500))*time.Millisecond), "", depth+1)
		}
		return t.Add(15 * time.Millisecond), ""
	}
	if name := cmp.Or(ch.Props["credentialName"], ch.Props["credential_name"]); name != "" && x.m.Credentials != nil && !x.m.hasCredential(name) {
		return t.Add(20 * time.Millisecond), fmt.Sprintf("com.sap.it.nm.security.SecurityException: credential %s not found on this tenant", name)
	}
	if u, ok := x.m.selfURL(address); ok { // a runtime endpoint of this tenant
		return x.callSelf(ch, msg, u, t, depth)
	}
	host := receiverHost(ch, address)
	sys, ok := x.system(host)
	latency := 100 * time.Millisecond
	if ok && sys.LatencyMs > 0 {
		latency = time.Duration(sys.LatencyMs) * time.Millisecond
	}
	latency += time.Duration(x.rng.IntN(int(latency/time.Millisecond)/4+1)) * time.Millisecond
	t = t.Add(latency)
	if ok && sys.FailRate > 0 && x.rng.Float64() < sys.FailRate {
		status := sys.Status
		if status == 0 {
			status = 500
		}
		text := sys.Error
		if text == "" {
			text = fmt.Sprintf("HTTP %d from {host}", status)
		}
		text = strings.NewReplacer("{host}", host, "{key}", msg.key, "{status}", fmt.Sprint(status)).Replace(text)
		return t, text
	}
	return t, ""
}

// receiverHost is the host a receiver channel calls.
func receiverHost(ch channel, address string) string {
	if h := resolve(ch.Props["host"], nil); h != "" && !strings.Contains(h, "{{") {
		return strings.Split(h, ":")[0]
	}
	if u, err := url.Parse(address); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return address
}

// system finds the system a host belongs to (first match by name order).
func (x *executor) system(host string) (SystemSpec, bool) {
	for _, name := range sortedKeys(x.systems) {
		s := x.systems[name]
		if ok, _ := path.Match(s.Match, host); ok {
			return s, true
		}
	}
	return SystemSpec{}, false
}

// hasCredential reports whether a security material entry of that name exists.
func (m *Tenant) hasCredential(name string) bool {
	for _, coll := range m.Credentials {
		if _, ok := coll[name]; ok {
			return true
		}
	}
	return false
}
