package cpi

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/go-errors/errors"
)

// Tenant-wide runtime information: integration connections (which systems
// the deployed flows connect), log file archives, external logging and
// archiving settings.

// IntegrationConnection is a connection between a sender and a receiver host
// through deployed integration flows, as the tenant computes it.
type IntegrationConnection struct {
	ID           int                   `json:"id"`
	SenderHost   string                `json:"senderHost"`
	ReceiverHost string                `json:"receiverHost"`
	Resolved     bool                  `json:"resolved"`
	Flows        []IntegrationConnFlow `json:"flows"`
}

// IntegrationConnFlow is a flow of a connection.
type IntegrationConnFlow struct {
	ID               string `json:"id"`
	Name             string `json:"name,omitempty"`
	SenderHostType   string `json:"senderHostType,omitempty"`
	ReceiverHostType string `json:"receiverHostType,omitempty"`
}

// IntegrationConnections lists the connections with their flows. The tenant
// requires the InvalidateCache header: refresh recomputes the connections
// from the deployed flows instead of answering from its cache.
func IntegrationConnections(exe *httpclnt.HTTPExecuter, refresh bool) ([]IntegrationConnection, error) {
	resp, err := exe.ExecGetRequest("/api/v1/IntegrationConnections?$expand=IntegrationFlows", map[string]string{
		"Accept": "application/json", "InvalidateCache": fmt.Sprint(refresh)})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_, err = exe.LogError(resp, "Get integration connections")
		return nil, err
	}
	body, err := exe.ReadRespBody(resp)
	if err != nil {
		return nil, err
	}
	var data struct {
		D struct {
			Results []struct {
				Id                       flexInt
				SenderHost, ReceiverHost string
				ResolvedConnection       bool
				IntegrationFlows         struct {
					Results []struct{ Id, Name, SenderHostType, ReceiverHostType string } `json:"results"`
				}
			} `json:"results"`
		} `json:"d"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, errors.Wrap(err, 0)
	}
	out := make([]IntegrationConnection, 0, len(data.D.Results))
	for _, r := range data.D.Results {
		c := IntegrationConnection{ID: int(r.Id), SenderHost: r.SenderHost, ReceiverHost: r.ReceiverHost, Resolved: r.ResolvedConnection, Flows: []IntegrationConnFlow{}}
		for _, f := range r.IntegrationFlows.Results {
			c.Flows = append(c.Flows, IntegrationConnFlow{ID: f.Id, Name: f.Name, SenderHostType: f.SenderHostType, ReceiverHostType: f.ReceiverHostType})
		}
		out = append(out, c)
	}
	return out, nil
}

// LogFileArchive is a downloadable zip of log files.
type LogFileArchive struct {
	Scope       string `json:"scope"`       // latest (the tenant answers 501 for all)
	LogFileType string `json:"logFileType"` // http, trace
	NodeScope   string `json:"nodeScope"`   // worker
	ContentType string `json:"contentType,omitempty"`
}

// LogFileArchives lists the archives the tenant offers.
func LogFileArchives(exe *httpclnt.HTTPExecuter) ([]LogFileArchive, error) {
	var rows []struct{ Scope, LogFileType, NodeScope, ContentType string }
	if err := getResults(exe, "/api/v1/LogFileArchives", "Get log file archives", &rows); err != nil {
		return nil, err
	}
	out := make([]LogFileArchive, 0, len(rows))
	for _, r := range rows {
		out = append(out, LogFileArchive{Scope: r.Scope, LogFileType: r.LogFileType, NodeScope: r.NodeScope, ContentType: r.ContentType})
	}
	return out, nil
}

// LogFileArchiveContent downloads an archive (zip).
func LogFileArchiveContent(exe *httpclnt.HTTPExecuter, a LogFileArchive) ([]byte, error) {
	return getValue(exe, fmt.Sprintf("/api/v1/LogFileArchives(Scope=%s,LogFileType=%s,NodeScope=%s)/$value",
		pathKey(a.Scope), pathKey(a.LogFileType), pathKey(a.NodeScope)), "Download log file archive")
}

// TenantSettings are tenant-wide switches of the runtime.
type TenantSettings struct {
	// ExternalLogging: message processing logs are also sent to an external
	// system (e.g. Splunk); nil when the tenant does not answer.
	ExternalLogging *bool `json:"externalLogging"`
	// Archiving: message processing logs are archived to a document store.
	Archiving *bool `json:"archiving"`
	// ArchivingRuns are the last archiving runs.
	ArchivingRuns []ArchivingRun `json:"archivingRuns"`
}

// ArchivingRun is one archiving run (ArchivingKeyPerformanceIndicators).
type ArchivingRun struct {
	RunStart            string `json:"runStart"`
	RunStatus           string `json:"runStatus"`
	RunFailedPhase      string `json:"runFailedPhase,omitempty"`
	MplsToBeArchived    int64  `json:"mplsToBeArchived"`
	MplsArchived        int64  `json:"mplsArchived"`
	MplsArchivingFailed int64  `json:"mplsArchivingFailed"`
	ArchivedUntil       string `json:"archivedUntil,omitempty"`
	RunMinutes          int64  `json:"runMinutes"`
}

// ExternalLoggingActive reads the external logging switch.
func ExternalLoggingActive(exe *httpclnt.HTTPExecuter) (bool, error) {
	var rows []struct{ Active bool }
	if err := getResults(exe, "/api/v1/ExternalLoggingActivationStatus", "Get external logging status", &rows); err != nil {
		return false, err
	}
	return len(rows) > 0 && rows[0].Active, nil
}

// ArchivingActive reads the archiving switch.
func ArchivingActive(exe *httpclnt.HTTPExecuter) (bool, error) {
	var rows []struct{ Active bool }
	if err := getResults(exe, "/api/v1/ArchivingConfigurations", "Get archiving configuration", &rows); err != nil {
		return false, err
	}
	return len(rows) > 0 && rows[0].Active, nil
}

// ArchivingRuns lists the last archiving runs.
func ArchivingRuns(exe *httpclnt.HTTPExecuter) ([]ArchivingRun, error) {
	var rows []struct {
		RunStart, RunStatus, RunFailedPhase, MplsArchivedUntilDate                string
		MplsToBeArchived, MplsArchived, MplsArchivingFailed, RunDurationInMinutes flexInt
	}
	if err := getResults(exe, "/api/v1/ArchivingKeyPerformanceIndicators", "Get archiving runs", &rows); err != nil {
		return nil, err
	}
	out := make([]ArchivingRun, 0, len(rows))
	for _, r := range rows {
		out = append(out, ArchivingRun{RunStart: r.RunStart, RunStatus: r.RunStatus, RunFailedPhase: r.RunFailedPhase,
			MplsToBeArchived: int64(r.MplsToBeArchived), MplsArchived: int64(r.MplsArchived), MplsArchivingFailed: int64(r.MplsArchivingFailed),
			ArchivedUntil: r.MplsArchivedUntilDate, RunMinutes: int64(r.RunDurationInMinutes)})
	}
	return out, nil
}

// SetExternalLogging switches external logging on or off.
func SetExternalLogging(exe *httpclnt.HTTPExecuter, active bool) error {
	fn := "deactivateExternalLogging"
	if active {
		fn = "activateExternalLogging"
	}
	return functionCall(exe, "/api/v1/"+fn, "Switch external logging")
}

// ActivateArchiving switches archiving of message processing logs on (the
// API offers no deactivation).
func ActivateArchiving(exe *httpclnt.HTTPExecuter) error {
	return functionCall(exe, "/api/v1/activateArchivingConfiguration", "Activate archiving")
}

func functionCall(exe *httpclnt.HTTPExecuter, path, callType string) error {
	resp, err := exe.Exec(http.MethodPost, path, http.NoBody, map[string]string{"Accept": "application/json"})
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, err = exe.LogError(resp, callType)
		return err
	}
	_ = resp.Body.Close()
	return nil
}
