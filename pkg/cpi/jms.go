package cpi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"
)

// JMS messages of a queue (Manage Message Queues in the Web UI). On Cloud
// Foundry the tenant serves them as MessagingMessages, reached through their
// queue: MessagingQueues('{queue}')/MessagingMessages (the MessagingMessages
// collection itself answers 404, the Neo-era JmsMessages and JmsQueues 501).
// An unknown queue name lists no messages instead of answering 404.

// JMSMessage is a message waiting in a JMS queue.
type JMSMessage struct {
	ID    string `json:"jmsMessageId"`
	Queue string `json:"queue"`
	// Failed: processing failed, the message waits for the next retry.
	Failed bool `json:"failed"`
	// MessageGuid is the message processing log of the last attempt.
	MessageGuid   string     `json:"messageGuid,omitempty"`
	CreatedAt     *time.Time `json:"createdAt,omitempty"`
	RetryCount    int64      `json:"retryCount"`
	NextRetry     *time.Time `json:"nextRetry,omitempty"`
	OverdueAt     *time.Time `json:"overdueAt,omitempty"`
	ExpiresAt     *time.Time `json:"expiresAt,omitempty"`
	Sender        string     `json:"sender,omitempty"`
	Receiver      string     `json:"receiver,omitempty"`
	MessageType   string     `json:"messageType,omitempty"`
	ApplicationID string     `json:"applicationId,omitempty"`
	CorrelationID string     `json:"correlationId,omitempty"`
	CustomStatus  string     `json:"customStatus,omitempty"`
}

// QueueMessages lists the messages of a JMS queue.
func (s *Stores) QueueMessages(queue string) ([]JMSMessage, error) {
	var rows []struct {
		JmsMessageID   string  `json:"jmsMessageId"`
		QueueName      string  `json:"queueName"`
		Failed         bool    `json:"failed"`
		MplID          string  `json:"mplId"`
		CreatedAt      flexInt `json:"createdAt"`
		RetryCount     flexInt `json:"retryCount"`
		NextRetry      flexInt `json:"nextRetry"`
		OverdueAt      flexInt `json:"overdueAt"`
		ExpirationDate flexInt `json:"expirationDate"`
		Sender         string  `json:"sender"`
		Receiver       string  `json:"receiver"`
		MessageType    string  `json:"messageType"`
		ApplicationID  string  `json:"applicationId"`
		CorrelationID  string  `json:"correlationId"`
		CustomStatus   string  `json:"customStatus"`
	}
	if err := getResults(s.exe, "/api/v1/MessagingQueues("+pathKey(queue)+")/MessagingMessages", "Get JMS messages", &rows); err != nil {
		return nil, err
	}
	out := make([]JMSMessage, 0, len(rows))
	for _, r := range rows {
		out = append(out, JMSMessage{ID: r.JmsMessageID, Queue: cmpOrString(r.QueueName, queue), Failed: r.Failed, MessageGuid: r.MplID,
			CreatedAt: millisTime(r.CreatedAt), RetryCount: int64(r.RetryCount), NextRetry: millisTime(r.NextRetry), OverdueAt: millisTime(r.OverdueAt),
			ExpiresAt: millisTime(r.ExpirationDate), Sender: r.Sender, Receiver: r.Receiver, MessageType: r.MessageType,
			ApplicationID: r.ApplicationID, CorrelationID: r.CorrelationID, CustomStatus: r.CustomStatus})
	}
	return out, nil
}

func cmpOrString(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// DeleteQueueMessage deletes one message from a queue
// (DELETE MessagingMessages(jmsMessageId,queueName)).
func (s *Stores) DeleteQueueMessage(queue, id string) error {
	path := fmt.Sprintf("/api/v1/MessagingMessages(jmsMessageId=%s,queueName=%s)", pathKey(id), pathKey(queue))
	_, err := s.send(http.MethodDelete, path, nil, "Delete JMS message")
	return err
}

// RetryQueueMessages asks the broker to process messages of a queue again now
// (function import RetryMessagingMessages). It returns the tenant's answer.
func (s *Stores) RetryQueueMessages(queue string, ids []string) (string, error) {
	body, err := json.Marshal(retryBody(queue, ids))
	if err != nil {
		return "", err
	}
	return s.send(http.MethodPost, "/api/v1/RetryMessagingMessages", body, "Retry JMS messages")
}

// MoveQueueMessages moves messages of a queue to another queue (function
// import MoveMessagingMessages), e.g. from an error queue back to the queue
// of the flow that processes them. It returns the tenant's answer.
func (s *Stores) MoveQueueMessages(queue, target string, ids []string) (string, error) {
	body, err := json.Marshal(moveBody(queue, target, ids))
	if err != nil {
		return "", err
	}
	return s.send(http.MethodPost, "/api/v1/MoveMessagingMessages", body, "Move JMS messages")
}

// The $metadata declares RetryMessagingMessages and MoveMessagingMessages
// without parameters, so the messages go in a JSON body named after the
// MessagingMessage properties. This format is not confirmed on a tenant yet.

// QueueMessagesBody is the request body of RetryMessagingMessages.
type QueueMessagesBody struct {
	QueueName     string   `json:"queueName"`
	JmsMessageIDs []string `json:"jmsMessageIds"`
}

// MoveMessagesBody is the request body of MoveMessagingMessages.
type MoveMessagesBody struct {
	QueueName       string   `json:"queueName"`
	TargetQueueName string   `json:"targetQueueName"`
	JmsMessageIDs   []string `json:"jmsMessageIds"`
}

func retryBody(queue string, ids []string) QueueMessagesBody {
	return QueueMessagesBody{QueueName: queue, JmsMessageIDs: slices.Clip(ids)}
}

func moveBody(queue, target string, ids []string) MoveMessagesBody {
	return MoveMessagesBody{QueueName: queue, TargetQueueName: target, JmsMessageIDs: slices.Clip(ids)}
}

// SetQueueActive activates or deactivates a queue (function imports
// activateQueue / deactivateQueue): a deactivated queue keeps its messages
// but hands none to its consumer.
func (s *Stores) SetQueueActive(queue string, active bool) (string, error) {
	fn := "deactivateQueue"
	if active {
		fn = "activateQueue"
	}
	return s.send(http.MethodPost, "/api/v1/"+fn+"?Name="+queryEscape(odataString(queue)), nil, "Set JMS queue state")
}

// send runs a modifying call that answers 200, 201, 202 or 204 and returns
// the answer's text (the d of a function import's Edm.String result, or the
// raw body).
func (s *Stores) send(method, path string, body []byte, callType string) (string, error) {
	headers := map[string]string{"Accept": "application/json"}
	var resp *http.Response
	var err error
	if body != nil {
		headers["Content-Type"] = "application/json"
		resp, err = s.exe.Exec(method, path, bytes.NewReader(body), headers)
	} else {
		resp, err = s.exe.Exec(method, path, http.NoBody, headers)
	}
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, err = s.exe.LogError(resp, callType)
		return "", err
	}
	b, err := s.exe.ReadRespBody(resp)
	if err != nil || len(b) == 0 {
		return "", err
	}
	var d struct {
		D json.RawMessage `json:"d"`
	}
	if json.Unmarshal(b, &d) == nil && len(d.D) > 0 {
		var text string
		if json.Unmarshal(d.D, &text) == nil {
			return text, nil
		}
		var fn map[string]string // {"d":{"activateQueue":"..."}}
		if json.Unmarshal(d.D, &fn) == nil && len(fn) == 1 {
			for _, v := range fn {
				return v, nil
			}
		}
		return string(d.D), nil
	}
	return string(b), nil
}
