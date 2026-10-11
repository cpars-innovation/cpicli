package ops

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// ConnectionList is the result of ListConnections.
type ConnectionList struct {
	Connections []cpi.IntegrationConnection `json:"connections"`
	// Hosts are the distinct sender and receiver hosts.
	Hosts []string `json:"hosts"`
}

// ListConnections lists the tenant's integration connections (sender host ->
// flows -> receiver host), optionally only those with a flow or host
// containing filter (case-insensitive).
func ListConnections(exe *httpclnt.HTTPExecuter, refresh bool, filter string) (*ConnectionList, error) {
	conns, err := cpi.IntegrationConnections(exe, refresh)
	if err != nil {
		return nil, err
	}
	f := strings.ToLower(filter)
	res := &ConnectionList{Connections: []cpi.IntegrationConnection{}, Hosts: []string{}}
	hosts := map[string]bool{}
	for _, c := range conns {
		if f != "" && !strings.Contains(strings.ToLower(c.SenderHost+" "+c.ReceiverHost), f) && !connHasFlow(c, f) {
			continue
		}
		res.Connections = append(res.Connections, c)
		for _, h := range []string{c.SenderHost, c.ReceiverHost} {
			if h != "" && !hosts[h] {
				hosts[h] = true
				res.Hosts = append(res.Hosts, h)
			}
		}
	}
	return res, nil
}

func connHasFlow(c cpi.IntegrationConnection, f string) bool {
	for _, fl := range c.Flows {
		if strings.Contains(strings.ToLower(fl.ID+" "+fl.Name), f) {
			return true
		}
	}
	return false
}

// LogArchiveResult is the result of DownloadLogArchive.
type LogArchiveResult struct {
	File      string               `json:"file"`
	Bytes     int                  `json:"bytes"`
	Archive   cpi.LogFileArchive   `json:"archive"`
	Available []cpi.LogFileArchive `json:"available"`
}

// DownloadLogArchive saves the zip of the latest log files of a type (http,
// trace) to file.
func DownloadLogArchive(exe *httpclnt.HTTPExecuter, logType, file string) (*LogArchiveResult, error) {
	if file == "" {
		return nil, output.Usagef("an output file is required")
	}
	archives, err := cpi.LogFileArchives(exe)
	if err != nil {
		return nil, err
	}
	var types []string
	for _, a := range archives {
		types = append(types, a.LogFileType)
		if strings.EqualFold(a.LogFileType, logType) {
			data, err := cpi.LogFileArchiveContent(exe, a)
			if err != nil {
				return nil, err
			}
			if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(file, data, 0o644); err != nil {
				return nil, err
			}
			return &LogArchiveResult{File: file, Bytes: len(data), Archive: a, Available: archives}, nil
		}
	}
	return nil, output.Usagef("the tenant offers no %q log archive (available: %s)", logType, strings.Join(types, ", "))
}

// TenantSettingsResult adds the reasons a setting could not be read.
type TenantSettingsResult struct {
	cpi.TenantSettings
	Warnings []string `json:"warnings,omitempty"`
}

// GetTenantSettings reads external logging, archiving and the last archiving
// runs; a part the credentials cannot read is reported as a warning.
func GetTenantSettings(exe *httpclnt.HTTPExecuter) *TenantSettingsResult {
	res := &TenantSettingsResult{TenantSettings: cpi.TenantSettings{ArchivingRuns: []cpi.ArchivingRun{}}}
	if on, err := cpi.ExternalLoggingActive(exe); err == nil {
		res.ExternalLogging = &on
	} else {
		res.Warnings = append(res.Warnings, "external logging: "+err.Error())
	}
	if on, err := cpi.ArchivingActive(exe); err == nil {
		res.Archiving = &on
	} else {
		res.Warnings = append(res.Warnings, "archiving: "+err.Error())
	}
	if runs, err := cpi.ArchivingRuns(exe); err == nil {
		res.ArchivingRuns = runs
	} else {
		res.Warnings = append(res.Warnings, "archiving runs: "+err.Error())
	}
	return res
}
