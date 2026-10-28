package ops

import (
	"fmt"
	"slices"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// JMS message management, as Manage Message Queues in the Web UI: list the
// messages of a queue, retry them, move them to another queue, delete them,
// activate or deactivate a queue.

// QueueMessageList is the result of ListQueueMessages.
type QueueMessageList struct {
	Queue     string           `json:"queue"`
	Messages  []cpi.JMSMessage `json:"messages"`
	Total     int              `json:"total"`
	Failed    int              `json:"failed"`
	Truncated bool             `json:"truncated,omitempty"`
}

// requireQueue returns a usage error unless the queue exists: the tenant
// lists no messages for an unknown queue name instead of answering 404.
func requireQueue(stores *cpi.Stores, names ...string) error {
	queues, err := stores.Queues()
	if err != nil {
		return err
	}
	for _, n := range names {
		if n == "" {
			return output.Usagef("queue name is required")
		}
		if !slices.ContainsFunc(queues, func(q cpi.Queue) bool { return q.Name == n }) {
			return output.Usagef("queue %s not found (cpictl jms queues / list_jms_queues lists the queues)", n)
		}
	}
	return nil
}

// ListQueueMessages lists the messages of a queue (failedOnly: only those
// whose processing failed and that wait for a retry), at most top (default
// 100). Payloads are not read.
func ListQueueMessages(exe *httpclnt.HTTPExecuter, queue string, failedOnly bool, top int) (*QueueMessageList, error) {
	if top <= 0 {
		top = 100
	}
	stores := cpi.NewStores(exe)
	if err := requireQueue(stores, queue); err != nil {
		return nil, err
	}
	msgs, err := stores.QueueMessages(queue)
	if err != nil {
		return nil, err
	}
	res := &QueueMessageList{Queue: queue, Messages: []cpi.JMSMessage{}}
	for _, m := range msgs {
		if m.Failed {
			res.Failed++
		}
		if failedOnly && !m.Failed {
			continue
		}
		res.Total++
		if len(res.Messages) == top {
			res.Truncated = true
			continue
		}
		res.Messages = append(res.Messages, m)
	}
	return res, nil
}

// JMSSelection selects messages of a queue: the given IDs, or all of them.
type JMSSelection struct {
	Queue string
	IDs   []string
	All   bool
	// FailedOnly with All: only failed messages.
	FailedOnly bool
}

// JMSActionResult is the outcome of a retry, move or delete.
type JMSActionResult struct {
	Action      string   `json:"action"`
	Queue       string   `json:"queue"`
	TargetQueue string   `json:"targetQueue,omitempty"`
	MessageIDs  []string `json:"messageIds"`
	// Response is the tenant's answer to retry and move.
	Response string `json:"response,omitempty"`
	// Note says what the action means for the messages.
	Note string `json:"note,omitempty"`
}

// retryNote: the tenant's "n message(s) processed" means handed back to the
// consumer, not delivered.
const retryNote = "the messages are handed back to their consumer flow; whether they succeed shows in list_jms_messages (retry count, failed) and the consumer's message logs"

// selectMessages resolves a selection to message IDs of the queue. Unknown
// IDs are usage errors, so a typo is not reported as success.
func selectMessages(stores *cpi.Stores, sel JMSSelection) ([]string, error) {
	if err := requireQueue(stores, sel.Queue); err != nil {
		return nil, err
	}
	if sel.All == (len(sel.IDs) > 0) {
		return nil, output.Usagef("give message IDs or all (not both)")
	}
	msgs, err := stores.QueueMessages(sel.Queue)
	if err != nil {
		return nil, err
	}
	var ids []string
	if sel.All {
		for _, m := range msgs {
			if !sel.FailedOnly || m.Failed {
				ids = append(ids, m.ID)
			}
		}
		if len(ids) == 0 {
			return nil, output.Usagef("queue %s has no matching messages", sel.Queue)
		}
		return ids, nil
	}
	for _, id := range sel.IDs {
		if !slices.ContainsFunc(msgs, func(m cpi.JMSMessage) bool { return m.ID == id }) {
			return nil, output.Usagef("message %s is not in queue %s", id, sel.Queue)
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// RetryQueueMessages processes messages of a queue again now, instead of
// waiting for the next retry.
func RetryQueueMessages(exe *httpclnt.HTTPExecuter, sel JMSSelection) (*JMSActionResult, error) {
	stores := cpi.NewStores(exe)
	ids, err := selectMessages(stores, sel)
	if err != nil {
		return nil, err
	}
	text, err := stores.RetryQueueMessages(sel.Queue, ids)
	if err != nil {
		return nil, err
	}
	return &JMSActionResult{Action: "RETRY", Queue: sel.Queue, MessageIDs: ids, Response: text, Note: retryNote}, nil
}

// MoveQueueMessages moves messages of a queue to another existing queue,
// e.g. from an error queue back to the queue of the flow that processes them.
func MoveQueueMessages(exe *httpclnt.HTTPExecuter, sel JMSSelection, target string) (*JMSActionResult, error) {
	stores := cpi.NewStores(exe)
	if target == "" {
		return nil, output.Usagef("target queue is required")
	}
	if target == sel.Queue {
		return nil, output.Usagef("target queue is the source queue")
	}
	if err := requireQueue(stores, target); err != nil {
		return nil, err
	}
	ids, err := selectMessages(stores, sel)
	if err != nil {
		return nil, err
	}
	text, err := stores.MoveQueueMessages(sel.Queue, target, ids)
	if err != nil {
		return nil, err
	}
	return &JMSActionResult{Action: "MOVE", Queue: sel.Queue, TargetQueue: target, MessageIDs: ids, Response: text}, nil
}

// DeleteQueueMessages deletes messages from a queue: they are not processed
// any more. MessageIDs lists the deleted ones also when a later one fails.
func DeleteQueueMessages(exe *httpclnt.HTTPExecuter, sel JMSSelection) (*JMSActionResult, error) {
	stores := cpi.NewStores(exe)
	ids, err := selectMessages(stores, sel)
	if err != nil {
		return nil, err
	}
	res := &JMSActionResult{Action: "DELETE", Queue: sel.Queue, MessageIDs: []string{}}
	for _, id := range ids {
		if err := stores.DeleteQueueMessage(sel.Queue, id); err != nil {
			err = fmt.Errorf("deleting %s from %s (%d of %d deleted): %w", id, sel.Queue, len(res.MessageIDs), len(ids), err)
			if len(res.MessageIDs) > 0 && !httpclnt.IsAuthError(err) {
				err = output.Partial(err)
			}
			return res, err
		}
		res.MessageIDs = append(res.MessageIDs, id)
	}
	return res, nil
}

// QueueStateResult is the outcome of SetQueueState.
type QueueStateResult struct {
	Queue    string `json:"queue"`
	Active   bool   `json:"active"`
	Response string `json:"response,omitempty"`
}

// SetQueueState activates or deactivates a queue. A deactivated queue keeps
// receiving and keeping messages, but hands none to its consumer flow.
func SetQueueState(exe *httpclnt.HTTPExecuter, queue string, active bool) (*QueueStateResult, error) {
	stores := cpi.NewStores(exe)
	if err := requireQueue(stores, queue); err != nil {
		return nil, err
	}
	text, err := stores.SetQueueActive(queue, active)
	if err != nil {
		return nil, err
	}
	return &QueueStateResult{Queue: queue, Active: active, Response: text}, nil
}
