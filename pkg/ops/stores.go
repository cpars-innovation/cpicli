package ops

import (
	"slices"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// DataStoreEntryList is the result of ListDataStoreEntries.
type DataStoreEntryList struct {
	Entries   []cpi.DataStoreEntry `json:"entries"`
	Total     int                  `json:"total"`
	Truncated bool                 `json:"truncated,omitempty"`
}

// ListDataStoreEntries lists entries of one data store (key.Name) or of all
// stores, optionally only those written by one message, at most top (default
// 100). Content is read separately (GetDataStoreEntry): it is business data.
func ListDataStoreEntries(exe *httpclnt.HTTPExecuter, key cpi.DataStoreKey, messageGuid string, overdueOnly bool, top int) (*DataStoreEntryList, error) {
	if top <= 0 {
		top = 100
	}
	entries, err := cpi.NewStores(exe).DataStoreEntries(key, messageGuid, overdueOnly)
	if err != nil {
		return nil, err
	}
	if key.Name == "" && key.IntegrationFlow != "" {
		entries = slices.DeleteFunc(entries, func(e cpi.DataStoreEntry) bool { return e.IntegrationFlow != key.IntegrationFlow })
	}
	res := &DataStoreEntryList{Entries: entries, Total: len(entries)}
	if len(entries) > top {
		res.Entries, res.Truncated = entries[:top], true
	}
	if res.Entries == nil {
		res.Entries = []cpi.DataStoreEntry{}
	}
	return res, nil
}

// GetDataStoreEntry downloads an entry's content (truncated to max bytes).
func GetDataStoreEntry(exe *httpclnt.HTTPExecuter, id string, key cpi.DataStoreKey, max int) (*DownloadedContent, error) {
	if id == "" || key.Name == "" {
		return nil, output.Usagef("id and data_store are required")
	}
	b, err := cpi.NewStores(exe).DataStoreEntryContent(id, key)
	if err != nil {
		return nil, err
	}
	return &DownloadedContent{ID: id, Content: NewContent(b, max)}, nil
}

// DeleteDataStoreEntry deletes an entry.
func DeleteDataStoreEntry(exe *httpclnt.HTTPExecuter, id string, key cpi.DataStoreKey) (map[string]string, error) {
	if id == "" || key.Name == "" {
		return nil, output.Usagef("id and data_store are required")
	}
	if err := cpi.NewStores(exe).DeleteDataStoreEntry(id, key); err != nil {
		return nil, err
	}
	return map[string]string{"id": id, "dataStore": key.Name, "action": "DELETED"}, nil
}

// ListVariables lists global variables and those of an integration flow
// (artifactID "": all).
func ListVariables(exe *httpclnt.HTTPExecuter, artifactID string) ([]cpi.Variable, error) {
	vars, err := cpi.NewStores(exe).Variables()
	if err != nil {
		return nil, err
	}
	if artifactID != "" {
		vars = slices.DeleteFunc(vars, func(v cpi.Variable) bool { return v.IntegrationFlow != "" && v.IntegrationFlow != artifactID })
	}
	if vars == nil {
		vars = []cpi.Variable{}
	}
	return vars, nil
}

// GetVariable downloads a variable's value (artifactID "" for a global one).
func GetVariable(exe *httpclnt.HTTPExecuter, name, artifactID string, max int) (*DownloadedContent, error) {
	if name == "" {
		return nil, output.Usagef("name is required")
	}
	b, err := cpi.NewStores(exe).VariableContent(name, artifactID)
	if err != nil {
		return nil, err
	}
	return &DownloadedContent{ID: name, Content: NewContent(b, max)}, nil
}

// ListQueues lists JMS queues, optionally with a name prefix, fullest first.
func ListQueues(exe *httpclnt.HTTPExecuter, prefix string) ([]cpi.Queue, error) {
	queues, err := cpi.NewStores(exe).Queues()
	if err != nil {
		return nil, err
	}
	queues = slices.DeleteFunc(queues, func(q cpi.Queue) bool { return !strings.HasPrefix(q.Name, prefix) })
	if len(queues) > 0 {
		// optional: which flows use the queue
		flows, err := cpi.NewStores(exe).QueueFlows()
		if err != nil && httpclnt.IsAuthError(err) {
			return nil, err
		}
		for i := range queues {
			queues[i].Flows = flows[queues[i].Name]
		}
	}
	slices.SortStableFunc(queues, func(a, b cpi.Queue) int { return int(b.Messages - a.Messages) })
	if queues == nil {
		queues = []cpi.Queue{}
	}
	return queues, nil
}

// LogFileList is the result of ListLogFiles.
type LogFileList struct {
	Files []cpi.LogFile `json:"files"`
}

// ListLogFiles lists log files (newest first), of a type and modified since.
func ListLogFiles(exe *httpclnt.HTTPExecuter, logType string, since time.Time) (*LogFileList, error) {
	files, err := cpi.NewStores(exe).LogFiles(logType)
	if err != nil {
		return nil, err
	}
	if !since.IsZero() {
		files = slices.DeleteFunc(files, func(f cpi.LogFile) bool { return f.LastModified != nil && f.LastModified.Before(since) })
	}
	if files == nil {
		files = []cpi.LogFile{}
	}
	return &LogFileList{Files: files}, nil
}

// LogFileTail is the end of a log file.
type LogFileTail struct {
	Name        string `json:"name"`
	Application string `json:"application"`
	Size        int    `json:"size"`
	// Offset is where the returned content starts in the file.
	Offset int `json:"offset"`
	Content
}

// GetLogFile returns the last tailBytes of a log file (default 64 KB, max
// 1 MB), starting at a line boundary.
func GetLogFile(exe *httpclnt.HTTPExecuter, name, application string, tailBytes int) (*LogFileTail, error) {
	if name == "" || application == "" {
		return nil, output.Usagef("name and application are required (from list_log_files)")
	}
	if tailBytes <= 0 {
		tailBytes = DefaultMaxContentBytes
	}
	b, err := cpi.NewStores(exe).LogFileContent(name, application)
	if err != nil {
		return nil, err
	}
	res := &LogFileTail{Name: name, Application: application, Size: len(b)}
	if len(b) > tailBytes {
		start := len(b) - tailBytes
		if nl := strings.IndexByte(string(b[start:]), '\n'); nl >= 0 && nl < len(b)-start-1 {
			start += nl + 1
		}
		res.Offset, b = start, b[start:]
	}
	res.Content = NewContent(b, -1)
	res.Content.Truncated = res.Offset > 0
	return res, nil
}

// ListIdempotentEntries lists idempotent repository entries: by entry ID
// (server side), and filtered by component and source.
func ListIdempotentEntries(exe *httpclnt.HTTPExecuter, id, component, source string) ([]cpi.IdempotentEntry, error) {
	entries, err := cpi.NewStores(exe).IdempotentEntries(id)
	if err != nil {
		return nil, err
	}
	entries = slices.DeleteFunc(entries, func(e cpi.IdempotentEntry) bool {
		return (component != "" && !strings.EqualFold(e.Component, component)) || (source != "" && !strings.Contains(e.Source, source))
	})
	if entries == nil {
		entries = []cpi.IdempotentEntry{}
	}
	return entries, nil
}

// ListIDMappings returns the ID mapper entries of a source or a target ID.
func ListIDMappings(exe *httpclnt.HTTPExecuter, sourceID, targetID string) ([]cpi.IDMapping, error) {
	if (sourceID == "") == (targetID == "") {
		return nil, output.Usagef("give either source_id or target_id")
	}
	m, err := cpi.NewStores(exe).IDMappings(sourceID, targetID)
	if m == nil && err == nil {
		m = []cpi.IDMapping{}
	}
	return m, err
}
