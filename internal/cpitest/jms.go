package cpitest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
)

// JMS queues and messages of the mock (MessagingQueues, MessagingMessages,
// RetryMessagingMessages, MoveMessagingMessages, activateQueue,
// deactivateQueue), and the custom header search
// (MessageProcessingLogCustomHeaderProperties with $filter and $expand=Log).

// JMSQueue is a JMS queue of the mock tenant.
type JMSQueue struct {
	Name     string       `json:"name"`
	Inactive bool         `json:"inactive,omitempty"`
	Messages []JMSMessage `json:"messages"`
}

// JMSMessage is a message waiting in a queue.
type JMSMessage struct {
	ID         string    `json:"id"`
	Failed     bool      `json:"failed"`
	MplID      string    `json:"mplId,omitempty"`
	Correlate  string    `json:"correlationId,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	RetryCount int       `json:"retryCount"`
	NextRetry  time.Time `json:"nextRetry"`
}

// queue returns the queue, creating it.
func (m *Tenant) queue(name string) *JMSQueue {
	if m.Queues == nil {
		m.Queues = map[string]*JMSQueue{}
	}
	q := m.Queues[name]
	if q == nil {
		q = &JMSQueue{Name: name, Messages: []JMSMessage{}}
		m.Queues[name] = q
	}
	return q
}

// syncQueues creates the queues that running flows read from (a queue
// exists once a consumer is deployed).
func (m *Tenant) syncQueues() {
	for _, id := range sortedKeys(m.Artifacts) {
		a := m.Artifacts[id]
		if !a.running() {
			continue
		}
		fi := a.info()
		if fi == nil {
			continue
		}
		for _, s := range fi.fm.Senders {
			if s.Adapter == "JMS" && s.Address != "" {
				m.queue(resolve(s.Address, a.Parameters))
			}
		}
	}
}

// addJMSRetry puts the message of a failed JMS consumer run back into its
// queue: it waits there for the next retry.
func (m *Tenant) addJMSRetry(queue string, l MessageLog) {
	q := m.queue(queue)
	q.Messages = append(q.Messages, JMSMessage{ID: "ID:mock:" + l.Guid, Failed: true, MplID: l.Guid, Correlate: l.CorrelationID,
		CreatedAt: l.Start, RetryCount: 1, NextRetry: l.End.Add(time.Minute)})
}

func millis(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return fmt.Sprint(t.UnixMilli())
}

func jmsMessageJSON(queue string, msg JMSMessage) map[string]any {
	return map[string]any{"jmsMessageId": msg.ID, "queueName": queue, "failed": msg.Failed, "mplId": msg.MplID,
		"createdAt": millis(msg.CreatedAt), "retryCount": fmt.Sprint(msg.RetryCount), "nextRetry": millis(msg.NextRetry),
		"correlationId": msg.Correlate}
}

var (
	reMessagingQueue   = regexp.MustCompile(`^/api/v1/MessagingQueues\('((?:[^']|'')+)'\)(/MessagingMessages)?$`)
	reMessagingMessage = regexp.MustCompile(`^/api/v1/MessagingMessages\(jmsMessageId='((?:[^']|'')+)',queueName='((?:[^']|'')+)'\)$`)
	reQueueFunction    = regexp.MustCompile(`^'((?:[^']|'')+)'$`)
	reHeaderFilter     = regexp.MustCompile(`^(Name|Value) eq '((?:[^']|'')*)'(?: and (Name|Value) eq '((?:[^']|'')*)')?$`)
)

// handleJMS serves the JMS endpoints; false if path is not one of them.
func (m *Tenant) handleJMS(w http.ResponseWriter, r *http.Request, path string) bool {
	switch {
	case r.Method == http.MethodGet && path == "/api/v1/MessagingQueues":
		m.syncQueues()
		rows := []map[string]any{}
		for _, name := range sortedKeys(m.Queues) {
			q := m.Queues[name]
			rows = append(rows, map[string]any{"queueName": name, "numberOfMessages": fmt.Sprint(len(q.Messages)), "active": !q.Inactive, "exclusive": false})
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": rows}})

	case r.Method == http.MethodGet && reMessagingQueue.MatchString(path):
		m.syncQueues()
		mm := reMessagingQueue.FindStringSubmatch(path)
		name := unquote(mm[1])
		q := m.Queues[name]
		if mm[2] == "" {
			if q == nil {
				odataError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("Queue '%s' not found.", name))
				return true
			}
			writeJSON(w, map[string]any{"d": map[string]any{"queueName": name, "numberOfMessages": fmt.Sprint(len(q.Messages)), "active": !q.Inactive}})
			return true
		}
		rows := []map[string]any{} // an unknown queue lists no messages, like the tenant
		if q != nil {
			for _, msg := range q.Messages {
				rows = append(rows, jmsMessageJSON(name, msg))
			}
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": rows}})

	case r.Method == http.MethodDelete && reMessagingMessage.MatchString(path):
		mm := reMessagingMessage.FindStringSubmatch(path)
		id, name := unquote(mm[1]), unquote(mm[2])
		q := m.Queues[name]
		i := -1
		if q != nil {
			i = slices.IndexFunc(q.Messages, func(msg JMSMessage) bool { return msg.ID == id })
		}
		if i < 0 {
			odataError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("Message '%s' not found in queue '%s'.", id, name))
			return true
		}
		q.Messages = slices.Delete(q.Messages, i, i+1)
		w.WriteHeader(http.StatusNoContent)

	case r.Method == http.MethodPost && (path == "/api/v1/RetryMessagingMessages" || path == "/api/v1/MoveMessagingMessages"):
		var body struct {
			QueueName       string   `json:"queueName"`
			TargetQueueName string   `json:"targetQueueName"`
			JmsMessageIDs   []string `json:"jmsMessageIds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.QueueName == "" || len(body.JmsMessageIDs) == 0 {
			odataError(w, http.StatusBadRequest, "Bad Request", "queueName and jmsMessageIds are required")
			return true
		}
		q := m.Queues[body.QueueName]
		move := strings.HasSuffix(path, "/MoveMessagingMessages")
		var target *JMSQueue
		if move {
			if target = m.Queues[body.TargetQueueName]; target == nil || body.TargetQueueName == body.QueueName {
				odataError(w, http.StatusBadRequest, "Bad Request", fmt.Sprintf("Target queue '%s' not found.", body.TargetQueueName))
				return true
			}
		}
		for _, id := range body.JmsMessageIDs {
			if q == nil || !slices.ContainsFunc(q.Messages, func(msg JMSMessage) bool { return msg.ID == id }) {
				odataError(w, http.StatusBadRequest, "Bad Request", fmt.Sprintf("Message '%s' not found in queue '%s'.", id, body.QueueName))
				return true
			}
		}
		now := time.Now()
		for _, id := range body.JmsMessageIDs {
			i := slices.IndexFunc(q.Messages, func(msg JMSMessage) bool { return msg.ID == id })
			if move {
				msg := q.Messages[i]
				msg.RetryCount, msg.NextRetry = 0, time.Time{}
				target.Messages = append(target.Messages, msg)
				q.Messages = slices.Delete(q.Messages, i, i+1)
				continue
			}
			q.Messages[i].RetryCount++
			q.Messages[i].NextRetry = now
		}
		fn := strings.TrimPrefix(path, "/api/v1/")
		writeJSON(w, map[string]any{"d": map[string]any{fn: fmt.Sprintf("%d message(s) processed", len(body.JmsMessageIDs))}})

	case r.Method == http.MethodPost && (path == "/api/v1/activateQueue" || path == "/api/v1/deactivateQueue"):
		mm := reQueueFunction.FindStringSubmatch(r.URL.Query().Get("Name"))
		m.syncQueues()
		if mm == nil || m.Queues[unquote(mm[1])] == nil {
			odataError(w, http.StatusBadRequest, "Bad Request", "Queue not found.")
			return true
		}
		fn := strings.TrimPrefix(path, "/api/v1/")
		m.Queues[unquote(mm[1])].Inactive = fn == "deactivateQueue"
		writeJSON(w, map[string]any{"d": map[string]any{fn: "OK"}})

	case r.Method == http.MethodGet && path == "/api/v1/MessageProcessingLogCustomHeaderProperties":
		if m.NoCustomHeaderSearch {
			odataError(w, http.StatusNotFound, "Not Found", "Could not find an entity set or function import for 'MessageProcessingLogCustomHeaderProperties'.")
			return true
		}
		m.customHeaderSearch(w, r)

	default:
		return false
	}
	return true
}

// customHeaderSearch serves MessageProcessingLogCustomHeaderProperties with
// $filter on Name and/or Value (eq) and $expand=Log.
func (m *Tenant) customHeaderSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var name, value *string
	if f := q.Get("$filter"); f != "" {
		mm := reHeaderFilter.FindStringSubmatch(f)
		if mm == nil || (mm[3] != "" && mm[3] == mm[1]) {
			odataError(w, http.StatusBadRequest, "Bad Request", fmt.Sprintf("Invalid filter expression: '%s'.", f))
			return
		}
		for i := 1; i <= 3; i += 2 {
			v := unquote(mm[i+1])
			switch mm[i] {
			case "Name":
				name = &v
			case "Value":
				value = &v
			}
		}
	}
	expand := q.Get("$expand") == "Log"
	rows := []map[string]any{}
	seen := map[string]bool{}
	for j := len(m.MessageLogSteps) - 1; j >= 0; j-- { // latest state of each message
		for _, l := range m.MessageLogSteps[j] {
			if seen[l.Guid] {
				continue
			}
			seen[l.Guid] = true
			for _, k := range sortedKeys(l.Headers) {
				if (name != nil && *name != k) || (value != nil && *value != l.Headers[k]) {
					continue
				}
				row := map[string]any{"Id": l.Guid + "/" + k, "Name": k, "Value": l.Headers[k]}
				if expand {
					row["Log"] = mplJSON(l)
				}
				rows = append(rows, row)
			}
		}
	}
	writeJSON(w, map[string]any{"d": map[string]any{"results": rows}})
}
