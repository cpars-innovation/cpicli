package cpi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// Operations data of the runtime (SAP API packages Message Stores, Log Files
// and Message Processing Logs): data stores, variables, JMS queues, number
// ranges, log files, idempotent repository and ID mapper entries.

// pathKey formats a string key for use in a URL path.
func pathKey(s string) string {
	return "'" + url.PathEscape(strings.ReplaceAll(s, "'", "''")) + "'"
}

// flexInt decodes integers that OData v2 sends as number or string.
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	b = bytes.Trim(b, `"`)
	if len(b) == 0 || string(b) == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseInt(string(b), 10, 64)
	*f = flexInt(n)
	return err
}

// Stores reads operations data of the runtime.
type Stores struct {
	exe *httpclnt.HTTPExecuter
}

// NewStores returns a Stores client.
func NewStores(exe *httpclnt.HTTPExecuter) *Stores { return &Stores{exe: exe} }

// DataStore is a data store of the runtime.
type DataStore struct {
	Name            string `json:"name"`
	IntegrationFlow string `json:"integrationFlow,omitempty"`
	Type            string `json:"type,omitempty"`
	Visibility      string `json:"visibility,omitempty"`
	Messages        int64  `json:"messages"`
	OverdueMessages int64  `json:"overdueMessages"`
}

// DataStores lists the data stores (overdueOnly: only those with overdue
// entries).
func (s *Stores) DataStores(overdueOnly bool) ([]DataStore, error) {
	path := "/api/v1/DataStores"
	if overdueOnly {
		path += "?overdueonly=true"
	}
	var rows []struct {
		DataStoreName, IntegrationFlow, Type, Visibility string
		NumberOfMessages, NumberOfOverdueMessages        flexInt
	}
	if err := getResults(s.exe, path, "Get data stores", &rows); err != nil {
		return nil, err
	}
	out := make([]DataStore, 0, len(rows))
	for _, r := range rows {
		out = append(out, DataStore{Name: r.DataStoreName, IntegrationFlow: r.IntegrationFlow, Type: r.Type, Visibility: r.Visibility,
			Messages: int64(r.NumberOfMessages), OverdueMessages: int64(r.NumberOfOverdueMessages)})
	}
	return out, nil
}

// DataStoreKey identifies a data store; IntegrationFlow and Type are empty for
// global stores.
type DataStoreKey struct {
	Name, IntegrationFlow, Type string
}

// DataStoreEntry is an entry of a data store.
type DataStoreEntry struct {
	ID              string     `json:"id"`
	DataStoreName   string     `json:"dataStore"`
	IntegrationFlow string     `json:"integrationFlow,omitempty"`
	Type            string     `json:"type,omitempty"`
	Status          string     `json:"status,omitempty"`
	MessageGuid     string     `json:"messageGuid,omitempty"`
	DueAt           *time.Time `json:"dueAt,omitempty"`
	CreatedAt       *time.Time `json:"createdAt,omitempty"`
	RetainUntil     *time.Time `json:"retainUntil,omitempty"`
}

func optTime(v string) *time.Time {
	t, err := ParseODataTime(v)
	if err != nil || t.IsZero() {
		return nil
	}
	return &t
}

// DataStoreEntries lists entries of one store (key.Name set) or of all
// stores, optionally only those written by a message (messageGuid).
func (s *Stores) DataStoreEntries(key DataStoreKey, messageGuid string, overdueOnly bool) ([]DataStoreEntry, error) {
	path := "/api/v1/DataStoreEntries"
	if key.Name != "" {
		path = fmt.Sprintf("/api/v1/DataStores(DataStoreName=%s,IntegrationFlow=%s,Type=%s)/Entries",
			pathKey(key.Name), pathKey(key.IntegrationFlow), pathKey(key.Type))
	}
	q := url.Values{}
	if messageGuid != "" {
		q.Set("messageid", messageGuid)
	}
	if overdueOnly {
		q.Set("overdueonly", "true")
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var rows []struct {
		Id, DataStoreName, IntegrationFlow, Type, Status, Messageid, DueAt, CreatedAt, RetainUntil string
	}
	if err := getResults(s.exe, path, "Get data store entries", &rows); err != nil {
		return nil, err
	}
	out := make([]DataStoreEntry, 0, len(rows))
	for _, r := range rows {
		out = append(out, DataStoreEntry{ID: r.Id, DataStoreName: r.DataStoreName, IntegrationFlow: r.IntegrationFlow, Type: r.Type,
			Status: r.Status, MessageGuid: r.Messageid, DueAt: optTime(r.DueAt), CreatedAt: optTime(r.CreatedAt), RetainUntil: optTime(r.RetainUntil)})
	}
	return out, nil
}

func dataStoreEntryPath(id string, key DataStoreKey) string {
	return fmt.Sprintf("/api/v1/DataStoreEntries(Id=%s,DataStoreName=%s,IntegrationFlow=%s,Type=%s)",
		pathKey(id), pathKey(key.Name), pathKey(key.IntegrationFlow), pathKey(key.Type))
}

// DataStoreEntryContent downloads the content of an entry.
func (s *Stores) DataStoreEntryContent(id string, key DataStoreKey) ([]byte, error) {
	return getValue(s.exe, dataStoreEntryPath(id, key)+"/$value", "Get data store entry content")
}

// DeleteDataStoreEntry deletes an entry.
func (s *Stores) DeleteDataStoreEntry(id string, key DataStoreKey) error {
	return modifyingCall("DELETE", dataStoreEntryPath(id, key), nil, 202, "Delete data store entry", s.exe)
}

// Variable is a global or integration flow variable.
type Variable struct {
	Name            string     `json:"name"`
	IntegrationFlow string     `json:"integrationFlow,omitempty"`
	Visibility      string     `json:"visibility,omitempty"`
	UpdatedAt       *time.Time `json:"updatedAt,omitempty"`
	RetainUntil     *time.Time `json:"retainUntil,omitempty"`
}

// Variables lists all variables.
func (s *Stores) Variables() ([]Variable, error) {
	var rows []struct{ VariableName, IntegrationFlow, Visibility, UpdatedAt, RetainUntil string }
	if err := getResults(s.exe, "/api/v1/Variables", "Get variables", &rows); err != nil {
		return nil, err
	}
	out := make([]Variable, 0, len(rows))
	for _, r := range rows {
		out = append(out, Variable{Name: r.VariableName, IntegrationFlow: r.IntegrationFlow, Visibility: r.Visibility,
			UpdatedAt: optTime(r.UpdatedAt), RetainUntil: optTime(r.RetainUntil)})
	}
	return out, nil
}

// VariableContent downloads the value of a variable (integrationFlow empty
// for global variables).
func (s *Stores) VariableContent(name, integrationFlow string) ([]byte, error) {
	return getValue(s.exe, fmt.Sprintf("/api/v1/Variables(VariableName=%s,IntegrationFlow=%s)/$value", pathKey(name), pathKey(integrationFlow)), "Get variable")
}

// Queue is a JMS queue.
type Queue struct {
	Name      string `json:"name"`
	Messages  int64  `json:"messages"`
	Active    bool   `json:"active"`
	Exclusive bool   `json:"exclusive"`
}

// Queues lists the JMS queues (MessagingQueues).
func (s *Stores) Queues() ([]Queue, error) {
	var rows []struct {
		QueueName        string  `json:"queueName"`
		NumberOfMessages flexInt `json:"numberOfMessages"`
		Active           bool    `json:"active"`
		Exclusive        bool    `json:"exclusive"`
	}
	if err := getResults(s.exe, "/api/v1/MessagingQueues", "Get JMS queues", &rows); err != nil {
		return nil, err
	}
	out := make([]Queue, 0, len(rows))
	for _, r := range rows {
		out = append(out, Queue{Name: r.QueueName, Messages: int64(r.NumberOfMessages), Active: r.Active, Exclusive: r.Exclusive})
	}
	return out, nil
}

// Broker is the capacity and usage of the JMS broker.
type Broker struct {
	Capacity        int64 `json:"capacity"`
	MaxCapacity     int64 `json:"maxCapacity"`
	Queues          int64 `json:"queues"`
	MaxQueues       int64 `json:"maxQueues"`
	CapacityOK      int64 `json:"capacityOk"`
	CapacityWarning int64 `json:"capacityWarning"`
	CapacityError   int64 `json:"capacityError"`
	// The flags are 1 when the broker reports a high number.
	TransactedSessionsHigh bool `json:"transactedSessionsHigh"`
	ConsumersHigh          bool `json:"consumersHigh"`
	ProducersHigh          bool `json:"producersHigh"`
}

// Broker reads JmsBrokers('Broker1').
func (s *Stores) Broker() (*Broker, error) {
	resp, err := readOnlyCall("/api/v1/JmsBrokers('Broker1')", "Get JMS broker", s.exe)
	if err != nil {
		return nil, err
	}
	body, err := s.exe.ReadRespBody(resp)
	if err != nil {
		return nil, err
	}
	var data struct {
		D struct {
			Capacity, MaxCapacity, QueueNumber, MaxQueueNumber, CapacityOk, CapacityWarning, CapacityError flexInt
			IsTransactedSessionsHigh, IsConsumersHigh, IsProducersHigh                                     flexInt
		} `json:"d"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}
	d := data.D
	return &Broker{Capacity: int64(d.Capacity), MaxCapacity: int64(d.MaxCapacity), Queues: int64(d.QueueNumber), MaxQueues: int64(d.MaxQueueNumber),
		CapacityOK: int64(d.CapacityOk), CapacityWarning: int64(d.CapacityWarning), CapacityError: int64(d.CapacityError),
		TransactedSessionsHigh: d.IsTransactedSessionsHigh == 1, ConsumersHigh: d.IsConsumersHigh == 1, ProducersHigh: d.IsProducersHigh == 1}, nil
}

// NumberRange is a number range object.
type NumberRange struct {
	Name         string     `json:"name"`
	Description  string     `json:"description,omitempty"`
	MinValue     string     `json:"minValue"`
	MaxValue     string     `json:"maxValue"`
	CurrentValue string     `json:"currentValue"`
	FieldLength  string     `json:"fieldLength,omitempty"`
	Rotate       string     `json:"rotate,omitempty"`
	DeployedBy   string     `json:"deployedBy,omitempty"`
	DeployedOn   *time.Time `json:"deployedOn,omitempty"`
}

// NumberRanges lists the number ranges.
func (s *Stores) NumberRanges() ([]NumberRange, error) {
	var rows []struct {
		Name, Description, MaxValue, MinValue, Rotate, CurrentValue, FieldLength, DeployedBy, DeployedOn string
	}
	if err := getResults(s.exe, "/api/v1/NumberRanges", "Get number ranges", &rows); err != nil {
		return nil, err
	}
	out := make([]NumberRange, 0, len(rows))
	for _, r := range rows {
		out = append(out, NumberRange{Name: r.Name, Description: r.Description, MinValue: r.MinValue, MaxValue: r.MaxValue, CurrentValue: r.CurrentValue,
			FieldLength: r.FieldLength, Rotate: r.Rotate, DeployedBy: r.DeployedBy, DeployedOn: optTime(r.DeployedOn)})
	}
	return out, nil
}

// LogFile is a system or HTTP log file of the runtime.
type LogFile struct {
	Name         string     `json:"name"`
	Application  string     `json:"application"`
	LogFileType  string     `json:"type,omitempty"`
	NodeScope    string     `json:"nodeScope,omitempty"`
	ContentType  string     `json:"contentType,omitempty"`
	Size         int64      `json:"size"`
	LastModified *time.Time `json:"lastModified,omitempty"`
}

// LogFiles lists log files, newest first, optionally of one type (http,
// trace, ...). The tenant refuses $orderby and $top on this collection
// (HTTP 501 on Cloud Foundry), so the list is sorted here.
func (s *Stores) LogFiles(logType string) ([]LogFile, error) {
	path := "/api/v1/LogFiles"
	if logType != "" {
		path += "?$filter=" + queryEscape("LogFileType eq "+odataString(logType))
	}
	var rows []struct {
		Name, Application, LastModified, ContentType, LogFileType, NodeScope string
		Size                                                                 flexInt
	}
	if err := getResults(s.exe, path, "Get log files", &rows); err != nil {
		return nil, err
	}
	out := make([]LogFile, 0, len(rows))
	for _, r := range rows {
		out = append(out, LogFile{Name: r.Name, Application: r.Application, LogFileType: r.LogFileType, NodeScope: r.NodeScope,
			ContentType: r.ContentType, Size: int64(r.Size), LastModified: optTime(r.LastModified)})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].LastModified, out[j].LastModified
		return a != nil && (b == nil || a.After(*b))
	})
	return out, nil
}

// LogFileContent downloads a log file.
func (s *Stores) LogFileContent(name, application string) ([]byte, error) {
	return getValue(s.exe, fmt.Sprintf("/api/v1/LogFiles(Name=%s,Application=%s)/$value", pathKey(name), pathKey(application)), "Get log file")
}

// IdempotentEntry is an entry of the idempotent repository (a message ID that
// was processed already and will be ignored when it arrives again).
type IdempotentEntry struct {
	Source         string     `json:"source"`
	Entry          string     `json:"entry"`
	Component      string     `json:"component,omitempty"`
	CreationTime   *time.Time `json:"creationTime,omitempty"`
	ExpirationTime *time.Time `json:"expirationTime,omitempty"`
}

func millisTime(v flexInt) *time.Time {
	if v <= 0 {
		return nil
	}
	t := time.UnixMilli(int64(v)).UTC()
	return &t
}

// IdempotentEntries lists idempotent repository entries, optionally with
// the given entry ID (SFTP: <directory>/<file>, XI: the message ID).
func (s *Stores) IdempotentEntries(id string) ([]IdempotentEntry, error) {
	path := "/api/v1/IdempotentRepositoryEntries"
	if id != "" {
		path += "?id=" + queryEscape(id)
	}
	var rows []struct {
		Source, Entry, Component     string
		CreationTime, ExpirationTime flexInt
	}
	if err := getResults(s.exe, path, "Get idempotent repository entries", &rows); err != nil {
		return nil, err
	}
	out := make([]IdempotentEntry, 0, len(rows))
	for _, r := range rows {
		out = append(out, IdempotentEntry{Source: r.Source, Entry: r.Entry, Component: r.Component,
			CreationTime: millisTime(r.CreationTime), ExpirationTime: millisTime(r.ExpirationTime)})
	}
	return out, nil
}

// IDMapping is an ID mapper entry.
type IDMapping struct {
	FromID         string `json:"fromId"`
	ToID           string `json:"toId"`
	Mapper         string `json:"mapper,omitempty"`
	Qualifier      string `json:"qualifier,omitempty"`
	Context        string `json:"context,omitempty"`
	ExpirationTime string `json:"expirationTime,omitempty"`
}

// IDMappings returns the target IDs mapped from sourceID, or (reverse) the
// source IDs mapped to targetID.
func (s *Stores) IDMappings(sourceID, targetID string) ([]IDMapping, error) {
	var out []IDMapping
	if sourceID != "" {
		var rows []struct{ ToId, FromId_, Mapper, ExpirationTime, Qualifier, Context string }
		if err := getResults(s.exe, fmt.Sprintf("/api/v1/IdMapFromIds(%s)/ToIds", pathKey(sourceID)), "Get ID mappings", &rows); err != nil {
			return nil, err
		}
		for _, r := range rows {
			from := r.FromId_
			if from == "" {
				from = sourceID
			}
			out = append(out, IDMapping{FromID: from, ToID: r.ToId, Mapper: r.Mapper, Qualifier: r.Qualifier, Context: r.Context, ExpirationTime: r.ExpirationTime})
		}
		return out, nil
	}
	var rows []struct{ FromId, ToId2, Mapper, ExpirationTime, Qualifier, Context string }
	if err := getResults(s.exe, fmt.Sprintf("/api/v1/IdMapToIds(%s)/FromId2s", pathKey(targetID)), "Get ID mappings", &rows); err != nil {
		return nil, err
	}
	for _, r := range rows {
		to := r.ToId2
		if to == "" {
			to = targetID
		}
		out = append(out, IDMapping{FromID: r.FromId, ToID: to, Mapper: r.Mapper, Qualifier: r.Qualifier, Context: r.Context, ExpirationTime: r.ExpirationTime})
	}
	return out, nil
}
