package mcp

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/internal/repo"
	"github.com/cpars-innovation/cpicli/internal/versioning"
	"github.com/cpars-innovation/cpicli/pkg/catalog"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/iflow"
	"github.com/cpars-innovation/cpicli/pkg/lint"
	"github.com/cpars-innovation/cpicli/pkg/ops"
)

// Instructions is sent to the client on initialize.
const Instructions = `Tools for SAP Cloud Integration (CPI) on one tenant. Unsure which tool or
skill fits a task? Call help (overview, or a topic: a tool, a skill and its instructions, a CLI
command). Auth or 403 errors: doctor shows which API areas the credentials can use.

Build loop: drift (local vs tenant: never overwrite tenant-only edits; compare shows per
artifact what differs between the tenant, a content tree and Git refs, with diffs) -> download_artifact
(existing flow, once), or copy_iflow (new flow from a template: new ID, name, sender addresses);
before a pull request bump_versions changed=true raises Bundle-Version of changed artifacts
-> edit the files locally -> layout_iflow (tidy the diagram after
editing an .iflw; never place shapes by hand) -> create_package (new
package) -> upload_artifact (upload_artifacts for several, in parallel) -> validate_artifact -> deploy -> send_test_message (wait_seconds=60)
-> on failure get_trace_tree (traceId of the result: the call tree across flows and firstFailure),
get_message_steps (failing step) and get_message_log / get_message_attachment /
get_message_store_entry for payloads; for step-by-step payloads set_log_level TRACE, send again,
get_message_trace and get_trace_message -> fix the local files and repeat.
Flows without an HTTP sender: ProcessDirect via send_test_message process_direct_address (test
harness flow); timer, SFTP and other polling flows: see the triggers in discover_tenant and use
list_message_logs with since and wait_seconds after triggering them.
Configuration: get_parameters / set_parameters, then deploy to activate; config_diff compares a
configure file with the tenant.
Review: check_guidelines (tenant design guidelines) before a release.
Promote: on the target tier's server, transport_check lists what the artifacts need and checks the
target (drafts, outside changes, dependencies, credentials, Partner Directory, parameter values)
before anything is uploaded; compare shows the differences between tiers.
Improve: lint checks local flows for reuse (scripts and mappings in several flows -> script
collections), Partner Directory candidates (routing tables, lookup tables, deployment copies),
dead weight, robustness and best practices; lint_fix moves scripts into collections and removes
unused scripts and unconnected steps (local files; the cpi-improve skill plans and applies them).
Inspect: list_packages, list_artifacts (cached briefly; refresh=true re-reads), list_resources,
get_resource (read without download). get_parameters artifact_ids reads several flows at once.
Operate: message_summary (volume and failures per flow, connection and error fingerprint over a
window: start here for "what is failing"), list_runtime_artifacts statuses=["ERROR"], get_runtime_status, list_message_logs,
list_service_endpoints; runtime data: list_data_stores, list_data_store_entries,
get_data_store_entry, delete_data_store_entry (confirm), list_variables, get_variable,
list_jms_queues, get_jms_broker, list_number_ranges, list_log_files / get_log_file (adapter errors
without a message log), list_idempotent_entries (skipped duplicates), list_id_mappings.
Components: list_components gives the adapter and step variants (cmdVariantUri) and versions the
tenant offers (.cpi/components.json); use them when writing flow XML, a newer version fails the
tenant's Check (lint rule component-newer-than-tenant).
Tenant: list_integration_connections (which systems the deployed flows connect, as the tenant
computes it), get_tenant_settings (external logging, archiving), download_log_archive (zip of
the latest http or trace logs).
find_messages_by_header finds messages by a business key the flows write (e.g. an order number).
JMS: list_jms_messages shows the messages of a queue (failed ones wait for a retry);
retry_jms_messages, move_jms_messages (confirm) and set_jms_queue_state act on them;
delete_jms_messages (confirm; offered only when the server's --tools names it) loses them for good.
Locks: list_locks shows artifacts open for editing (an update fails while someone holds the
lock). unlock_artifact (confirm; offered only when the server's --tools names it) discards the
lock holder's unsaved changes: only on the user's explicit request.
Value mappings: list_value_mappings, get_value_mapping (schemas with value pairs),
set_value_mapping (confirm) adds or changes a pair; deploy with artifact_type ValueMapping to use it.
Deleting: delete_artifact and delete_package (confirm; offered only when --tools names them)
cannot be undone and refuse deployed artifacts unless undeploy_first: only on the user's request.
Conventions: discover_tenant writes an inventory of existing flows (adapters, steps, error
handling, scripts, naming); follow the conventions of the repository (e.g. .cpi/conventions.md).
Graph: after discovery, graph_search, graph_neighbors and graph_path answer which flows call a
flow (ProcessDirect/JMS), use a credential, script, header or Partner Directory parameter, or
call a system, from .cpi/graph.json (local, fast; rerun discover_tenant after changes).

Autonomous loops: loop_start before changing anything, loop_status, loop_end when done or stopped.

Every result has {ok, errorCategory, exitCode, error, result}; errorCategory: usage (fix the
arguments), auth (stop, ask the user), tenant_http (retry later), failed (the tenant rejected the
content or the message failed: read error, fix), timeout (check status), partial (see items),
stopped (a loop limit was reached: stop changing things, call loop_end and report).
undeploy requires confirm=true. Partner Directory: pd_dependencies (which flows read a parameter), get_pd_parameters (tenant
values), pd_diff (local vs tenant), then
pd_deploy keys=["PID:ID"] to change one parameter without redeploying flows. pd_deploy is a
dry run unless dry_run=false. send_test_message
triggers real processing, including receiver calls. list_pd_partners lists the partners
(parameter counts, content types); list_alternative_partners / list_authorized_users and
create_alternative_partner / create_authorized_user manage partner identification.
Security material: list_credentials (including OAuth2 authorization code credentials),
list_keystore, list_keystore_history, get_certificate_chain, get_ssh_public_key, list_pgp_keys,
list_access_policies, list_audit_logs (who changed what; status missing_role = no role, do not
retry). Certificates: generate_key_pair (the tenant creates the key) -> get_csr -> the user has
it signed -> upload_certificate_chain. create_access_policy / add_artifact_reference restrict
artifacts to a role. Secrets and private keys never pass through this server (credentials, SSH
key import and PGP keyrings: ask the user to run cpictl). The delete tools (delete_keystore_entry,
delete_access_policy, delete_artifact_reference, delete_alternative_partner,
delete_authorized_user) need confirm and are offered only when --tools names them.`

// Config configures the CPI tools.
type Config struct {
	Exe *httpclnt.HTTPExecuter
	// Root confines local paths (artifact dirs, partner directory) to this
	// directory tree.
	Root string
	// PollInterval and MaxChecks are the defaults for deploy/undeploy polling.
	PollInterval time.Duration
	MaxChecks    int
	// LogPollInterval is the interval used by list_message_logs wait_seconds.
	LogPollInterval time.Duration
	// NewEndpointExecuter connects to runtime endpoints for send_test_message
	// (nil: the tool reports that runtime credentials are not configured).
	NewEndpointExecuter ops.EndpointExecuterFunc
	// TenantHost is recorded as the source of discover_tenant.
	TenantHost string
	// DenyFullSync makes pd_deploy refuse full_sync (develop mode).
	DenyFullSync bool
	// Versioning applies to upload_artifact and deploy (--versioning of the
	// server, see docs/versioning.md).
	Versioning versioning.Mode
	// LogLevels reverts set_log_level changes; created by Tools when nil.
	// Call RevertAll when the server stops.
	LogLevels *LogLevelReverter
	// CacheTTL keeps list_packages and list_artifacts results this long
	// (0: no cache); any tool that changes the tenant clears the cache.
	CacheTTL time.Duration
}

// Tools returns the CPI tool set.
func Tools(cfg Config) []Tool {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 10 * time.Second
	}
	if cfg.MaxChecks == 0 {
		cfg.MaxChecks = 30
	}
	if cfg.LogPollInterval == 0 {
		cfg.LogPollInterval = 5 * time.Second
	}
	readOnly := map[string]any{"readOnlyHint": true, "openWorldHint": true}
	tenant := ops.NewTenant(cfg.Exe)
	endpoints := cachedEndpointExecuters(cfg.NewEndpointExecuter)
	if cfg.LogLevels == nil {
		cfg.LogLevels = NewLogLevelReverter(cfg.Exe)
	}
	reverter := cfg.LogLevels
	cache := newReadCache(cfg.CacheTTL)
	tools := slices.Concat(toolList(cfg, readOnly, tenant, endpoints, reverter, cache), storeTools(cfg, readOnly), lockTools(cfg, readOnly), componentTools(cfg, readOnly), platformTools(cfg, readOnly), designTools(cfg, readOnly), operateTools(cfg, readOnly), securityTools(cfg, readOnly), graphTools(cfg))
	for i := range tools {
		inner := tools[i].Handler
		changesTenant := toolEffects[tools[i].Name] == EffectTenant
		tools[i].Handler = func(ctx context.Context, raw json.RawMessage) (any, error) {
			reverter.RunDue()
			if changesTenant {
				defer cache.clear()
			}
			return inner(ctx, raw)
		}
	}
	return tools
}

// batchParallel is the number of items a batch tool processes at a time.
const batchParallel = 8

// forEachParallel calls fn(0..n-1), parallel at a time, and returns the
// number of calls that failed.
func forEachParallel(n, parallel int, fn func(i int) error) int {
	var wg sync.WaitGroup
	var failed atomic.Int64
	slots := make(chan struct{}, parallel)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			if fn(i) != nil {
				failed.Add(1)
			}
		}()
	}
	wg.Wait()
	return int(failed.Load())
}

// batchErr is the error of a batch call: partial when some items failed,
// failed when all did.
func batchErr(failed, total int) error {
	switch {
	case failed == 0:
		return nil
	case failed == total:
		return output.Failed(fmt.Errorf("all %d item(s) failed", total))
	}
	return output.Partial(fmt.Errorf("%d of %d item(s) failed", failed, total))
}

// withCached marks a result served from the read cache.
func withCached(res map[string]any, cached bool) map[string]any {
	if cached {
		res["cached"] = true
	}
	return res
}

func toolList(cfg Config, readOnly map[string]any, tenant ops.Tenant, endpoints ops.EndpointExecuterFunc, reverter *LogLevelReverter, cache *readCache) []Tool {

	return []Tool{
		{
			Name: "doctor", Title: "Check the connection and roles",
			Description: "Check this server's tenant connection and which API areas its credentials can use: designtime, runtime, message logs, security material, keystore, Partner Directory, data stores, log files. " +
				"Per area ok, forbidden (403: the credentials lack the role) or missing (404: not offered by the tenant), and which tools need it. Use it when calls fail with auth or 403 errors. Only reads.",
			InputSchema: object(nil),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				if err := decode(raw, &struct{}{}); err != nil {
					return nil, err
				}
				res := ops.Doctor(ctx, cfg.Exe)
				return res, res.Err
			},
		},
		{
			Name: "list_packages", Title: "List integration packages",
			Description: "List all integration packages (ID, name, version). Start here to find where artifacts live, then list_artifacts. To add a package use create_package.",
			InputSchema: object(props{"refresh": boolean("Read the tenant even if the result of a recent call is cached")}),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Refresh bool `json:"refresh"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				pkgs, cached, err := cache.get("packages", a.Refresh, func() (any, error) { return ops.ListPackages(cfg.Exe) })
				return withCached(map[string]any{"packages": pkgs}, cached), err
			},
		},
		{
			Name: "list_artifacts", Title: "List artifacts of a package",
			Description: "List the designtime artifacts of one package: integration flows, message mappings, script collections and value mappings, with version and draft flag. Use it to find artifact IDs; for what is running use list_runtime_artifacts.",
			InputSchema: object(props{"package_id": str("Integration package ID"),
				"refresh": boolean("Read the tenant even if the result of a recent call is cached")}, "package_id"),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					PackageID string `json:"package_id"`
					Refresh   bool   `json:"refresh"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if a.PackageID == "" {
					return nil, output.Usagef("package_id is required")
				}
				arts, cached, err := cache.get("artifacts/"+a.PackageID, a.Refresh, func() (any, error) { return ops.ListArtifacts(cfg.Exe, a.PackageID) })
				return withCached(map[string]any{"packageId": a.PackageID, "artifacts": arts}, cached), err
			},
		},
		{
			Name: "get_runtime_status", Title: "Get runtime status",
			Description: "Runtime state of given artifacts: deployed or not, STARTED/STARTING/ERROR, version, deployment time and, for ERROR, the tenant's error message. Use it for known artifacts (e.g. after a deploy timeout); to find all broken deployments use list_runtime_artifacts with statuses [\"ERROR\"].",
			InputSchema: object(props{"artifact_ids": strArray("Artifact IDs")}, "artifact_ids"),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					ArtifactIDs []string `json:"artifact_ids"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				ids, err := requireIDs(a.ArtifactIDs)
				if err != nil {
					return nil, err
				}
				statuses := ops.GetRuntimeStatus(cfg.Exe, ids)
				var errs []string
				var firstErr error
				for _, s := range statuses {
					if s.Err != nil {
						errs = append(errs, s.ID+": "+s.Error)
						if firstErr == nil {
							firstErr = s.Err
						}
					}
				}
				result := map[string]any{"artifacts": statuses}
				if firstErr != nil {
					return result, fmt.Errorf("status lookup failed for %s: %w", strings.Join(errs, "; "), firstErr)
				}
				return result, nil
			},
		},
		{
			Name: "list_message_logs", Title: "Query message processing logs",
			Description: "Message processing logs (newest first) filtered by artifact, status, time and IDs, with the error text of failed messages. " +
				"Use it to see how an iFlow behaved at runtime: call with artifact_id, since=<time before the messages> and wait_seconds to wait until the messages reached a final status. " +
				"After send_test_message prefer its wait_seconds, which already returns the log of that message. " +
				"custom_header {name, value} finds messages by a custom header property (as find_messages_by_header; tenants without that search are scanned: needs artifact_id or package_id, and since; at most top*10, max 500 messages; see scanned/truncated).",
			InputSchema: object(props{
				"artifact_id":            str("Integration flow ID"),
				"statuses":               map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": cpi.MessageLogStatuses}, "description": "Only these statuses"},
				"since":                  str("Messages that ended after this time: RFC 3339 timestamp or duration back from now (30m, 2h, 1d)"),
				"until":                  str("Messages that started before this time (same format as since)"),
				"correlation_id":         str("Correlation ID"),
				"application_message_id": str("Application message ID"),
				"top":                    map[string]any{"type": "integer", "minimum": 1, "maximum": ops.MaxMessageLogs, "description": "Maximum number of messages, default 20"},
				"skip":                   integer("Skip the first n messages"),
				"include_errors":         boolean("Include the error text of failed messages (default true)"),
				"wait_seconds":           map[string]any{"type": "integer", "minimum": 0, "maximum": 600, "description": "Wait up to this long until at least one message matches and all matches are final"},
				"package_id":             str("Messages of all integration flows of this package (needs since)"),
				"custom_header": map[string]any{"type": "object", "description": "Only messages with this custom header property value", "additionalProperties": false,
					"properties": map[string]any{"name": str("Property name"), "value": str("Property value")}, "required": []string{"name", "value"}},
			}),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					ArtifactID           string                  `json:"artifact_id"`
					Statuses             []string                `json:"statuses"`
					Since                string                  `json:"since"`
					Until                string                  `json:"until"`
					CorrelationID        string                  `json:"correlation_id"`
					ApplicationMessageID string                  `json:"application_message_id"`
					Top                  int                     `json:"top"`
					Skip                 int                     `json:"skip"`
					IncludeErrors        *bool                   `json:"include_errors"`
					WaitSeconds          int                     `json:"wait_seconds"`
					PackageID            string                  `json:"package_id"`
					CustomHeader         *ops.CustomHeaderFilter `json:"custom_header"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				now := time.Now()
				since, err := ops.ParseTimeArg(a.Since, now)
				if err != nil {
					return nil, output.Usagef("invalid since: %v", err)
				}
				until, err := ops.ParseTimeArg(a.Until, now)
				if err != nil {
					return nil, output.Usagef("invalid until: %v", err)
				}
				q := ops.MessageLogQuery{ArtifactID: a.ArtifactID, Statuses: a.Statuses, Since: since, Until: until,
					CorrelationID: a.CorrelationID, ApplicationMessageID: a.ApplicationMessageID, Top: a.Top, Skip: a.Skip,
					IncludeErrors: a.IncludeErrors == nil || *a.IncludeErrors}
				if a.CustomHeader != nil || a.PackageID != "" {
					if a.WaitSeconds > 0 || a.CorrelationID != "" || a.ApplicationMessageID != "" || a.Skip > 0 {
						return nil, output.Usagef("custom_header and package_id cannot be combined with wait_seconds, correlation_id, application_message_id or skip")
					}
					scope := ops.ScanScope{PackageID: a.PackageID, Since: since, Until: until, Statuses: a.Statuses}
					if a.ArtifactID != "" {
						scope.ArtifactIDs = []string{a.ArtifactID}
					}
					if a.CustomHeader == nil {
						return ops.QueryPackageMessageLogs(cfg.Exe, scope, a.Top)
					}
					return ops.SearchMessagesByHeader(ctx, cfg.Exe, ops.HeaderSearchQuery{Header: *a.CustomHeader, ArtifactIDs: scope.ArtifactIDs, PackageID: a.PackageID,
						Since: since, Until: until, Statuses: a.Statuses, Top: a.Top, IncludeErrors: q.IncludeErrors})
				}
				if a.WaitSeconds > 0 {
					if a.WaitSeconds > 600 {
						return nil, output.Usagef("wait_seconds must be at most 600")
					}
					return ops.WaitForMessageLogs(ctx, cfg.Exe, q, time.Duration(a.WaitSeconds)*time.Second, cfg.LogPollInterval)
				}
				return ops.QueryMessageLogs(cfg.Exe, q)
			},
		},
		{
			Name: "get_message_log", Title: "Get one message processing log",
			Description: "Details of one message: status, full error text, errorInformation (lastErrorModelStepId: the .iflw element where it failed), custom header properties, adapter attributes (e.g. HTTP status), attachments and persisted messages. Next: get_message_steps for the failing step, get_message_attachment / get_message_store_entry for payloads.",
			InputSchema: object(props{"message_guid": str("Message GUID from list_message_logs")}, "message_guid"),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					MessageGuid string `json:"message_guid"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				return ops.GetMessageLog(cfg.Exe, a.MessageGuid, 0)
			},
		},
		{
			Name: "get_message_steps", Title: "Get processing steps of a message",
			Description: "Step-level trace of a message (runs and steps with status and error) and the first failing step. Use it for FAILED messages: modelStepId is the id of the element in the .iflw model to fix. properties=true adds each step's properties (e.g. adapter addresses).",
			InputSchema: object(props{"message_guid": str("Message GUID"), "properties": boolean("Include the properties of each step")}, "message_guid"),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					MessageGuid string `json:"message_guid"`
					Properties  bool   `json:"properties"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				return ops.GetMessageStepsWith(cfg.Exe, a.MessageGuid, a.Properties)
			},
		},
		{
			Name: "get_message_attachment", Title: "Download a message log attachment",
			Description: "Content of a message log attachment (id from get_message_log attachments), e.g. a payload logged by a script. Text inline, binary base64; truncated to max_bytes.",
			InputSchema: object(props{"attachment_id": str("Attachment ID"), "max_bytes": maxBytesSchema()}, "attachment_id"),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					AttachmentID string `json:"attachment_id"`
					MaxBytes     int    `json:"max_bytes"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if err := checkMaxBytes(a.MaxBytes); err != nil {
					return nil, err
				}
				return ops.GetMessageAttachment(cfg.Exe, a.AttachmentID, a.MaxBytes)
			},
		},
		{
			Name: "get_message_store_entry", Title: "Download a persisted message",
			Description: "Payload written by a Persist step (id from messageStoreEntries of get_message_log). Truncated to max_bytes.",
			InputSchema: object(props{"entry_id": str("Message store entry ID"), "max_bytes": maxBytesSchema()}, "entry_id"),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					EntryID  string `json:"entry_id"`
					MaxBytes int    `json:"max_bytes"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if err := checkMaxBytes(a.MaxBytes); err != nil {
					return nil, err
				}
				return ops.GetMessageStoreEntry(cfg.Exe, a.EntryID, a.MaxBytes)
			},
		},
		{
			Name: "set_log_level", Title: "Set the log level of a deployed flow",
			Description: "Set the message processing log level of a deployed integration flow: NONE, INFO, DEBUG or TRACE. " +
				"TRACE records payload and headers at every step for the next 10 minutes (then the tenant resets it): set it, send the message again, then get_message_trace. " +
				"Traces contain business data; use on development tenants.",
			InputSchema: object(props{
				"artifact_id":         str("Integration flow ID (deployed)"),
				"level":               enum("Log level", ops.LogLevels...),
				"node_type":           str(`Runtime node type, default "IFLMAP"`),
				"runtime_location_id": str(`Runtime location, default "cloudintegration" (edge integration cells use their own)`),
				"revert_after_minutes": map[string]any{"type": "integer", "minimum": 0, "maximum": 1440,
					"description": "Set the flow back to INFO after this many minutes (on the next tool call, or when the server stops); default 10 for TRACE and DEBUG, 0 = never"},
			}, "artifact_id", "level"),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					ArtifactID        string `json:"artifact_id"`
					Level             string `json:"level"`
					NodeType          string `json:"node_type"`
					RuntimeLocationID string `json:"runtime_location_id"`
					RevertAfter       *int   `json:"revert_after_minutes"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				req := ops.LogLevelRequest{ArtifactID: a.ArtifactID, Level: a.Level, NodeType: a.NodeType, RuntimeLocationID: a.RuntimeLocationID}
				res, err := ops.SetLogLevel(cfg.Exe, req)
				if err != nil {
					return res, err
				}
				minutes := 0
				if res.Level == "TRACE" || res.Level == "DEBUG" {
					minutes = 10
				}
				if a.RevertAfter != nil {
					minutes = *a.RevertAfter
				}
				if minutes > 0 && res.Level != "INFO" {
					at := reverter.now().UTC().Add(time.Duration(minutes) * time.Minute).Truncate(time.Second)
					res.RevertsAt = &at
					reverter.schedule(req, at)
				} else {
					reverter.schedule(req, time.Time{})
				}
				return res, nil
			},
		},
		{
			Name: "get_message_trace", Title: "List traced steps of a message",
			Description: "Steps of a message that was processed with log level TRACE, with the trace IDs of the message at each step (optionally only one model step). " +
				"Use it to see how the payload changed step by step; read one with get_trace_message. Empty with a hint when the flow was not on TRACE.",
			InputSchema: object(props{
				"message_guid":  str("Message GUID"),
				"model_step_id": str("Only this element of the .iflw model (modelStepId from get_message_steps)"),
			}, "message_guid"),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					MessageGuid string `json:"message_guid"`
					ModelStepID string `json:"model_step_id"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				return ops.GetMessageTrace(cfg.Exe, a.MessageGuid, a.ModelStepID)
			},
		},
		{
			Name: "get_trace_message", Title: "Read a traced message",
			Description: "Payload, headers and exchange properties of the message at one traced step (trace ID from get_message_trace). Sensitive header values are masked; the payload is truncated to max_bytes.",
			InputSchema: object(props{"trace_id": str("Trace ID from get_message_trace"), "max_bytes": maxBytesSchema()}, "trace_id"),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					TraceID  string `json:"trace_id"`
					MaxBytes int    `json:"max_bytes"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if err := checkMaxBytes(a.MaxBytes); err != nil {
					return nil, err
				}
				return ops.GetTraceMessage(cfg.Exe, a.TraceID, a.MaxBytes)
			},
		},
		{
			Name: "message_summary", Title: "Summarize messages per flow, connection and error",
			Description: "Message volume and failures of a time window: per flow the count per status, failures, average and longest duration; per connection between flows the messages and failures " +
				"(linked by the logs' predecessor, or for the content graph's connections by shared correlation ID, with .cpi/graph.json); failures grouped by error fingerprint with a sample, count, first and last occurrence and the newest message GUID. " +
				"Start here for \"what is failing\" questions, then get_trace_tree or get_message_log for one message. Read-only; reads at most max_messages (default 5000).",
			InputSchema: object(props{
				"since":         str("Start of the window: duration like 1h or RFC 3339 (default 1h)"),
				"until":         str("End of the window (default now)"),
				"artifacts":     strArray("Only these flows (IDs or patterns)"),
				"max_messages":  integer("Messages read at most, newest first (default 5000)"),
				"error_samples": integer("Error texts read for fingerprints (default 50)"),
			}),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Since        string   `json:"since"`
					Until        string   `json:"until"`
					Artifacts    []string `json:"artifacts"`
					MaxMessages  int      `json:"max_messages"`
					ErrorSamples int      `json:"error_samples"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				now := time.Now()
				since, err := ops.ParseTimeArg(cmp.Or(a.Since, "1h"), now)
				if err != nil {
					return nil, output.Usagef("invalid since: %v", err)
				}
				until, err := ops.ParseTimeArg(a.Until, now)
				if err != nil {
					return nil, output.Usagef("invalid until: %v", err)
				}
				q := ops.MessageSummaryQuery{Since: since, Until: until, Artifacts: a.Artifacts, MaxMessages: a.MaxMessages, ErrorSamples: a.ErrorSamples}
				if root, err := resolvePath(cfg.Root, "."); err == nil {
					if g, err := ops.LoadGraph(filepath.Join(root, ".cpi", "graph.json")); err == nil {
						q.Graph = g
					}
				}
				return ops.SummarizeMessages(cfg.Exe, q)
			},
		},
		{
			Name: "get_trace_tree", Title: "Get the path of a message or trace across flows",
			Description: "The call tree across flows, the hops from flow to flow, a pathKey (the same for every message taking the same route) and firstFailure, the earliest failed message. Give trace_id or message_guid. " +
				"trace_id (traceId of send_test_message, or trace-id custom header): messages by ApplicationMessageId first; otherwise scans the scope (artifact_ids or package_id, and since) for the trace-id custom header, at most max_scan messages. " +
				"message_guid (any message, e.g. a failed one): all messages with its correlation ID; with key_headers (custom header properties the flows write, such as an order number) messages with the same value join, " +
				"with the rest of their run, so the path continues when the message left the tenant and came back (found by the custom header search; a scope narrows it; tenants without that search scan the scope). " +
				"Hop links: span, predecessor, inferred (same correlation ID, by start time), header (joined by a key header). Next: get_message_steps for firstFailure.messageGuid.",
			InputSchema: object(props{
				"trace_id":     str("32 hex characters"),
				"message_guid": str("A message GUID: follow its correlation ID across flows"),
				"key_headers":  strArray("With message_guid: custom header properties that join runs with the same value"),
				"artifact_ids": strArray("Scope of the fallback scan"),
				"package_id":   str("Scope of the fallback scan: all flows of this package"),
				"since":        str("Start of the scan window: RFC 3339 or duration back from now (30m, 2h)"),
				"until":        str("End of the scan window"),
				"max_scan":     map[string]any{"type": "integer", "minimum": 1, "maximum": 1000, "description": "Maximum messages scanned, default 200"},
				"properties": map[string]any{"type": "object", "additionalProperties": false, "description": "Custom header names of the tracer (default trace-id, span-id, parent-span-id)",
					"properties": map[string]any{"trace": str("Trace ID property"), "span": str("Span ID property"), "parent": str("Parent span ID property")}},
			}),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					TraceID     string   `json:"trace_id"`
					MessageGuid string   `json:"message_guid"`
					KeyHeaders  []string `json:"key_headers"`
					ArtifactIDs []string `json:"artifact_ids"`
					PackageID   string   `json:"package_id"`
					Since       string   `json:"since"`
					Until       string   `json:"until"`
					MaxScan     int      `json:"max_scan"`
					Properties  *struct {
						Trace  string `json:"trace"`
						Span   string `json:"span"`
						Parent string `json:"parent"`
					} `json:"properties"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				now := time.Now()
				since, err := ops.ParseTimeArg(a.Since, now)
				if err != nil {
					return nil, output.Usagef("invalid since: %v", err)
				}
				until, err := ops.ParseTimeArg(a.Until, now)
				if err != nil {
					return nil, output.Usagef("invalid until: %v", err)
				}
				q := ops.TraceTreeQuery{TraceID: a.TraceID, MaxScan: a.MaxScan,
					Scope: ops.ScanScope{ArtifactIDs: a.ArtifactIDs, PackageID: a.PackageID, Since: since, Until: until}}
				if a.Properties != nil {
					q.Properties = ops.TraceProperties{Trace: a.Properties.Trace, Span: a.Properties.Span, Parent: a.Properties.Parent}
				}
				if (a.TraceID == "") == (a.MessageGuid == "") {
					return nil, output.Usagef("give either trace_id or message_guid")
				}
				if a.MessageGuid != "" {
					return ops.MessagePathFor(ctx, cfg.Exe, ops.MessagePathQuery{MessageGuid: a.MessageGuid, KeyHeaders: a.KeyHeaders,
						Scope: q.Scope, MaxScan: a.MaxScan, Properties: q.Properties})
				}
				return ops.TraceTreeFor(ctx, cfg.Exe, q)
			},
		},
		{
			Name: "list_runtime_artifacts", Title: "List deployed artifacts",
			Description: "All artifacts deployed on the tenant with status, version and deployment time. Use statuses [\"ERROR\"] for a tenant-wide health check (includes error messages); for specific artifacts use get_runtime_status.",
			InputSchema: object(props{"statuses": map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"STARTED", "STARTING", "ERROR", "STOPPING"}}, "description": "Only artifacts in these runtime statuses (default: all)"}}),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Statuses []string `json:"statuses"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				list, err := ops.ListRuntimeArtifacts(cfg.Exe, a.Statuses)
				return map[string]any{"artifacts": list}, err
			},
		},
		{
			Name: "list_service_endpoints", Title: "List endpoint URLs",
			Description: "Callable URLs of deployed integration flows with HTTP-based senders (HTTPS, SOAP, ...). send_test_message finds the URL itself; use this to show URLs or to choose between several endpoints of one flow.",
			InputSchema: object(props{"artifact_id": str("Only endpoints of this integration flow")}),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					ArtifactID string `json:"artifact_id"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				eps, err := ops.ListServiceEndpoints(cfg.Exe, a.ArtifactID)
				return map[string]any{"endpoints": eps}, err
			},
		},
		{
			Name: "validate_artifact", Title: "Validate an integration flow",
			Description: "Tenant check of an uploaded integration flow (Check in the Web UI): model errors such as missing mandatory settings. Checks the designtime version on the tenant, not local files: run it after upload_artifact and before deploy. status PASSED or FAILED (errorCategory failed) with details.",
			InputSchema: object(props{"artifact_id": str("Integration flow ID"), "version": str(`Designtime version, default "active"`)}, "artifact_id"),
			Annotations: map[string]any{"readOnlyHint": true, "openWorldHint": true},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					ArtifactID string `json:"artifact_id"`
					Version    string `json:"version"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				return ops.ValidateArtifact(cfg.Exe, a.ArtifactID, a.Version)
			},
		},
		{
			Name: "check_guidelines", Title: "Check design guidelines",
			Description: "Run the design guidelines activated on the tenant against an integration flow and wait for the result; returns violations with the violated components. Slower than validate_artifact: use it before a review or release, not after every edit.",
			InputSchema: object(props{
				"artifact_id":     str("Integration flow ID"),
				"version":         str(`Designtime version, default "active"`),
				"timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 600, "description": "Maximum wait, default 120"},
			}, "artifact_id"),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					ArtifactID     string `json:"artifact_id"`
					Version        string `json:"version"`
					TimeoutSeconds int    `json:"timeout_seconds"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				timeout := 120 * time.Second
				if a.TimeoutSeconds > 0 {
					timeout = time.Duration(a.TimeoutSeconds) * time.Second
				}
				return ops.CheckGuidelines(ctx, cfg.Exe, a.ArtifactID, a.Version, timeout, cfg.LogPollInterval)
			},
		},
		{
			Name: "list_resources", Title: "List resources of an integration flow",
			Description: "Resources inside an integration flow on the tenant (scripts, mappings, schemas) with name and type. To read one file use get_resource; to edit the flow use download_artifact.",
			InputSchema: object(props{"artifact_id": str("Integration flow ID"), "version": str(`Designtime version, default "active"`)}, "artifact_id"),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					ArtifactID string `json:"artifact_id"`
					Version    string `json:"version"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				list, err := ops.ListResources(cfg.Exe, a.ArtifactID, a.Version)
				return map[string]any{"artifactId": a.ArtifactID, "resources": list}, err
			},
		},
		{
			Name: "get_resource", Title: "Read a resource of an integration flow",
			Description: "Content of one resource of an integration flow on the tenant (e.g. a Groovy script or XSLT) without downloading the flow; text inline, binary base64.",
			InputSchema: object(props{
				"artifact_id": str("Integration flow ID"),
				"name":        str("Resource name from list_resources"),
				"type":        str("Resource type from list_resources (e.g. groovy, xslt, mmap)"),
				"version":     str(`Designtime version, default "active"`),
				"max_bytes":   maxBytesSchema(),
			}, "artifact_id", "name", "type"),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					ArtifactID string `json:"artifact_id"`
					Name       string `json:"name"`
					Type       string `json:"type"`
					Version    string `json:"version"`
					MaxBytes   int    `json:"max_bytes"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if err := checkMaxBytes(a.MaxBytes); err != nil {
					return nil, err
				}
				return ops.GetResource(cfg.Exe, a.ArtifactID, a.Version, a.Name, a.Type, a.MaxBytes)
			},
		},
		{
			Name: "download_artifact", Title: "Download an artifact into a local directory",
			Description: "Download a designtime artifact and extract it into a local directory inside the server root. Use it once before editing an existing artifact; then edit the files and upload_artifact. The directory must be empty unless overwrite=true.",
			InputSchema: object(props{
				"artifact_id":   str("Artifact ID"),
				"artifact_type": enum(`Artifact type, default "Integration"`, cpi.ArtifactTypes...),
				"dir":           str("Target directory, relative to the server root"),
				"overwrite":     boolean("Replace the content of a non-empty directory"),
				"version":       str(`Designtime version, default "active"`),
			}, "artifact_id", "dir"),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": true, "openWorldHint": true},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					ArtifactID   string `json:"artifact_id"`
					ArtifactType string `json:"artifact_type"`
					Dir          string `json:"dir"`
					Overwrite    bool   `json:"overwrite"`
					Version      string `json:"version"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if a.ArtifactType == "" {
					a.ArtifactType = "Integration"
				}
				dir, err := resolvePath(cfg.Root, a.Dir)
				if err != nil {
					return nil, err
				}
				if dir == mustAbs(cfg.Root) {
					return nil, output.Usagef("dir must be a sub-directory of the server root")
				}
				return ops.DownloadArtifactToDir(cfg.Exe, a.ArtifactType, a.ArtifactID, a.Version, dir, a.Overwrite)
			},
		},
		{
			Name: "copy_iflow", Title: "Copy an integration flow under a new ID",
			Description: "Copy an integration flow (a template or reference flow, from the tenant with from_artifact_id or from a local from_dir) into target_dir and rename it: " +
				"Bundle-SymbolicName, Bundle-Name, Bundle-Version (default 1.0.0) in MANIFEST.MF, description in metainfo.prop, <SourceID>.iflw -> <id>.iflw, .project, " +
				"and the sender addresses (in the model, or in parameters.prop for a {{parameter}} address). Every sender address needs a new value in addresses " +
				`(["NEW"] for a flow with one sender address, else "OLD=NEW" each, OLD as in the source) unless keep_addresses: the same HTTP path or ProcessDirect ` +
				"address fails the deployment, the same directory or queue makes both flows poll it. A value with \"=\" is always OLD=NEW. " +
				"Use this instead of renaming files by hand. The result lists the changes and files that still mention the source ID (check them). " +
				"Writes local files inside the server root only; next upload_artifact (name from the manifest), validate_artifact, deploy.",
			InputSchema: object(props{
				"from_artifact_id": str("Source flow ID on the tenant (read only)"),
				"from_version":     str(`Version of the source on the tenant, default "active"`),
				"from_dir":         str("Local source flow directory, relative to the server root (instead of from_artifact_id)"),
				"target_dir":       str("Target directory relative to the server root; must not exist or be empty"),
				"id":               str("ID of the new flow, following the naming conventions"),
				"name":             str("Display name of the new flow (default: the ID)"),
				"description":      str("Description of the new flow (the source's description is removed when empty)"),
				"version":          str(`Version of the new flow, default "1.0.0"`),
				"addresses":        strArray(`New sender addresses: ["NEW"] or ["OLD=NEW", ...]`),
				"keep_addresses":   boolean("Allow sender addresses that stay the same as in the source"),
			}, "target_dir", "id"),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": true},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					FromArtifactID string   `json:"from_artifact_id"`
					FromVersion    string   `json:"from_version"`
					FromDir        string   `json:"from_dir"`
					TargetDir      string   `json:"target_dir"`
					ID             string   `json:"id"`
					Name           string   `json:"name"`
					Description    string   `json:"description"`
					Version        string   `json:"version"`
					Addresses      []string `json:"addresses"`
					KeepAddresses  bool     `json:"keep_addresses"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				target, err := resolvePath(cfg.Root, a.TargetDir)
				if err != nil {
					return nil, err
				}
				if target == mustAbs(cfg.Root) {
					return nil, output.Usagef("target_dir must be a sub-directory of the server root")
				}
				o := ops.IFlowCopyOptions{FromID: a.FromArtifactID, FromVersion: a.FromVersion, TargetDir: target, ID: a.ID, Name: a.Name,
					Description: a.Description, Version: a.Version, Addresses: a.Addresses, KeepAddresses: a.KeepAddresses}
				if a.FromDir != "" {
					if o.FromDir, err = resolvePath(cfg.Root, a.FromDir); err != nil {
						return nil, err
					}
				}
				res, err := ops.CopyIFlow(cfg.Exe, o)
				if err != nil {
					return nil, err
				}
				res.Dir = a.TargetDir
				if a.FromDir != "" {
					res.From = a.FromDir
				}
				return res, nil
			},
		},
		{
			Name: "bump_versions", Title: "Raise Bundle-Version of changed artifacts",
			Description: "Raise Bundle-Version in META-INF/MANIFEST.MF of the artifacts below dir (layout <package>/<artifact>, inside the server root). " +
				"changed=true bumps only artifacts whose directory changed since the Git commit that last set their version (never-committed artifacts and " +
				"artifacts already bumped since that commit are left alone). Run it before a pull request when the repository uses versioning manifest; " +
				"commit the manifests with the change. Local files only; dry_run reports without writing.",
			InputSchema: object(props{
				"dir":       str("Content tree relative to the server root"),
				"changed":   boolean("Only artifacts changed since their version was last set (Git)"),
				"level":     enum("patch (default), minor or major", "patch", "minor", "major"),
				"packages":  strArray("Only these package folders (names or patterns)"),
				"artifacts": strArray("Only these artifact IDs (names or patterns)"),
				"dry_run":   boolean("Report the new versions without writing"),
			}, "dir"),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": false},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Dir       string   `json:"dir"`
					Changed   bool     `json:"changed"`
					Level     string   `json:"level"`
					Packages  []string `json:"packages"`
					Artifacts []string `json:"artifacts"`
					DryRun    bool     `json:"dry_run"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				dir, err := resolvePath(cfg.Root, a.Dir)
				if err != nil {
					return nil, err
				}
				return ops.BumpVersions(ctx, ops.BumpOptions{Dir: dir, Changed: a.Changed, Level: a.Level, Packages: a.Packages, Artifacts: a.Artifacts, DryRun: a.DryRun})
			},
		},
		{
			Name: "lint", Title: "Check flows for reuse, dead weight and best practices",
			Description: "Check the integration flows of a local content tree (<package>/<artifact>, inside the server root) and return findings with rule, severity, file, step and suggestion. " +
				"Groups: reuse (scripts or mappings in several flows -> script collection or shared mapping), partner-directory (routers on many literal values, lookup tables in scripts, flows deployed several times with different configOverrides), " +
				"dead-weight (unconnected steps, unused scripts, resources, parameters, no-op content modifiers), simplify, robustness (no exception subprocess, swallowed exceptions), performance (payload logging, body as string), " +
				"configuration (fixed receiver addresses, URLs and secrets in scripts), hygiene. All flows are read for cross-flow rules; packages/artifacts select what is reported. " +
				"Settings: .cpi/lint.yaml under the root; known findings: .cpi/lint-baseline.json (baseline=true on a finding). Local files only. Use it for the cpi-improve skill; fixable findings: lint_fix.",
			InputSchema: object(props{
				"dir":          str("Content tree relative to the server root (default: packages if it exists, else the root)"),
				"packages":     strArray("Only report these packages (names or patterns)"),
				"artifacts":    strArray("Only report these artifacts (names or patterns)"),
				"min_severity": enum("Do not report findings below this severity (default info)", "info", "warning", "error"),
				"rules":        strArray("Only these rules (default: all enabled in .cpi/lint.yaml)"),
				"list_rules":   boolean("Only list the rules"),
			}),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Dir         string   `json:"dir"`
					Packages    []string `json:"packages"`
					Artifacts   []string `json:"artifacts"`
					MinSeverity string   `json:"min_severity"`
					Rules       []string `json:"rules"`
					ListRules   bool     `json:"list_rules"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if a.ListRules {
					return map[string]any{"rules": lint.Rules()}, nil
				}
				o, err := mcpLintOptions(cfg.Root, a.Dir, a.Packages, a.Artifacts)
				if err != nil {
					return nil, err
				}
				o.MinSeverity = a.MinSeverity
				res, err := lint.Run(ctx, o)
				if err != nil || len(a.Rules) == 0 {
					return res, err
				}
				keep := res.Findings[:0]
				for _, f := range res.Findings {
					if slices.Contains(a.Rules, f.Rule) {
						keep = append(keep, f)
					}
				}
				res.Findings = keep
				return res, nil
			},
		},
		{
			Name: "lint_fix", Title: "Apply lint fixes to local files",
			Description: "Apply the mechanical fixes of lint to the local files (inside the server root): scripts used by several flows move into a script collection (the package's {package}_Scripts, or the shared collection with scriptCollections.crossPackage in .cpi/lint.yaml) and the flows reference it; " +
				"scripts equal to one in an existing collection reference it; unused scripts are deleted; unconnected steps are removed. rules=[\"noop-content-modifier\"] also removes content modifiers that do nothing. " +
				"dry_run lists the changes without writing. Nothing is sent to the tenant: afterwards bump_versions, upload (collections first), validate, deploy and test.",
			InputSchema: object(props{
				"dir":       str("Content tree relative to the server root (default: packages if it exists, else the root)"),
				"packages":  strArray("Only these packages"),
				"artifacts": strArray("Only these artifacts"),
				"rules":     strArray(`Rules to fix (default: duplicate-script, use-script-collection, unused-script, unconnected-step; ["all"]: every fixable rule)`),
				"dry_run":   boolean("Only list the changes"),
			}),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Dir       string   `json:"dir"`
					Packages  []string `json:"packages"`
					Artifacts []string `json:"artifacts"`
					Rules     []string `json:"rules"`
					DryRun    bool     `json:"dry_run"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				o, err := mcpLintOptions(cfg.Root, a.Dir, a.Packages, a.Artifacts)
				if err != nil {
					return nil, err
				}
				return lint.Fix(ctx, o, lint.FixOptions{Rules: a.Rules, DryRun: a.DryRun})
			},
		},
		{
			Name: "layout_iflow", Title: "Lay out flow diagrams",
			Description: "Recompute the diagram of local .iflw files (inside the server root): steps left to right in flow order, branches one below the other, exception subprocesses below the main flow, senders and receivers next to their steps, right-angled lines, no overlaps. " +
				"Only the diagram changes, never steps, configuration or sequence flows. Call it after editing an .iflw and before upload_artifact, instead of placing shapes by hand. " +
				"mode tidy (default) keeps the order of steps and branches, full also reorders branches; check only reports problems; dry_run writes nothing. Spacing from .cpi/lint.yaml (layout).",
			InputSchema: object(props{
				"paths":   strArray(".iflw files or directories (artifact, package, content tree) relative to the server root"),
				"mode":    enum("Layout mode (default tidy)", iflow.LayoutTidy, iflow.LayoutFull),
				"check":   boolean("Only report layout problems (missing shapes, overlaps, lines through steps)"),
				"dry_run": boolean("Compute the layout but write nothing"),
			}, "paths"),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Paths  []string `json:"paths"`
					Mode   string   `json:"mode"`
					Check  bool     `json:"check"`
					DryRun bool     `json:"dry_run"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if len(a.Paths) == 0 {
					return nil, output.Usagef("paths is required")
				}
				paths := make([]string, len(a.Paths))
				for i, p := range a.Paths {
					abs, err := resolvePath(cfg.Root, p)
					if err != nil {
						return nil, err
					}
					paths[i] = abs
				}
				base, err := resolvePath(cfg.Root, ".")
				if err != nil {
					return nil, err
				}
				lc, err := lint.LoadConfig(filepath.Join(base, ".cpi", "lint.yaml"))
				if err != nil {
					return nil, err
				}
				o := lc.Layout
				if a.Mode != "" {
					o.Mode = a.Mode
				}
				rep, err := iflow.LayoutPaths(paths, o, a.DryRun, a.Check)
				if err != nil {
					return nil, output.Usage(err)
				}
				for i := range rep.Files { // paths relative to the root, as given
					if rel, err := filepath.Rel(base, filepath.FromSlash(rep.Files[i].Path)); err == nil {
						rep.Files[i].Path = filepath.ToSlash(rel)
					}
				}
				return rep, nil
			},
		},
		{
			Name: "list_credentials", Title: "List security credentials",
			Description: "Names and metadata of user credentials, OAuth2 client credentials and secure parameters deployed on the tenant (never secrets). Use it to check that the credentials an iFlow references exist. Credentials cannot be created through MCP; ask the user to run 'cpictl credentials'.",
			InputSchema: object(props{"kind": enum("Only this kind", cpi.CredentialKinds...)}),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Kind string `json:"kind"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				return ops.ListCredentials(cfg.Exe, a.Kind)
			},
		},
		{
			Name: "list_keystore", Title: "List keystore entries",
			Description: "Certificates and key pairs of a tenant keystore with validity and days left; flags entries expiring within expiring_within_days. Use it to check that a key alias an adapter references exists and is valid.",
			InputSchema: object(props{
				"keystore":             enum(`Keystore, default "system"`, cpi.Keystores...),
				"expiring_within_days": map[string]any{"type": "integer", "minimum": 0, "maximum": 3650, "description": "Flag entries expiring within this many days"},
			}),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Keystore           string `json:"keystore"`
					ExpiringWithinDays int    `json:"expiring_within_days"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				return ops.ListKeystore(cfg.Exe, a.Keystore, time.Duration(a.ExpiringWithinDays)*24*time.Hour, false, time.Now())
			},
		},
		{
			Name: "get_parameters", Title: "Get configuration parameters",
			Description: "Externalised parameters ({{...}} placeholders in the model) of an integration flow with their current values. Change them with set_parameters; no upload needed. " +
				"artifact_ids reads several flows in one call (in parallel), one entry per flow.",
			InputSchema: object(props{"artifact_id": str("Integration flow ID"), "artifact_ids": strArray("Several integration flow IDs instead of artifact_id"),
				"version": str(`Designtime version, default "active"`)}),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					ArtifactID  string   `json:"artifact_id"`
					ArtifactIDs []string `json:"artifact_ids"`
					Version     string   `json:"version"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if (a.ArtifactID == "") == (len(a.ArtifactIDs) == 0) {
					return nil, output.Usagef("give artifact_id or artifact_ids")
				}
				if a.ArtifactID != "" {
					params, err := ops.GetConfiguration(cfg.Exe, a.ArtifactID, a.Version)
					return map[string]any{"artifactId": a.ArtifactID, "parameters": params}, err
				}
				ids, err := requireIDs(a.ArtifactIDs)
				if err != nil {
					return nil, err
				}
				type entry struct {
					ArtifactID string `json:"artifactId"`
					Parameters any    `json:"parameters,omitempty"`
					Error      string `json:"error,omitempty"`
				}
				entries := make([]entry, len(ids))
				failed := forEachParallel(len(ids), batchParallel, func(i int) error {
					params, err := ops.GetConfiguration(cfg.Exe, ids[i], a.Version)
					entries[i] = entry{ArtifactID: ids[i]}
					if err != nil {
						entries[i].Error = err.Error()
					} else {
						entries[i].Parameters = params
					}
					return err
				})
				return map[string]any{"artifacts": entries}, batchErr(failed, len(ids))
			},
		},
		{
			Name: "set_parameters", Title: "Set configuration parameters",
			Description: "Set externalised parameters of an integration flow. Only changed values are written; unknown keys fail the call before anything is written. The running version keeps the old values until you deploy.",
			InputSchema: object(props{
				"artifact_id": str("Integration flow ID"),
				"parameters":  map[string]any{"type": "object", "description": "Parameter key -> new value", "additionalProperties": map[string]any{"type": "string"}},
				"version":     str(`Designtime version, default "active"`),
				"dry_run":     boolean("Only report what would change"),
			}, "artifact_id", "parameters"),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					ArtifactID string            `json:"artifact_id"`
					Parameters map[string]string `json:"parameters"`
					Version    string            `json:"version"`
					DryRun     bool              `json:"dry_run"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if a.ArtifactID == "" || len(a.Parameters) == 0 {
					return nil, output.Usagef("artifact_id and at least one parameter are required")
				}
				return ops.UpdateConfiguration(cfg.Exe, a.ArtifactID, a.Version, a.Parameters, a.DryRun)
			},
		},
		{
			Name: "create_package", Title: "Create an integration package",
			Description: "Create an integration package if it does not exist (action CREATED or EXISTS; an existing package is never changed). Use it before upload_artifact of an artifact in a new package.",
			InputSchema: object(props{
				"package_id":  str("Package ID: letters and digits only, no special characters"),
				"name":        str("Display name, defaults to package_id"),
				"description": str("Description"),
				"short_text":  str("Short description, defaults to name"),
			}, "package_id"),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					PackageID   string `json:"package_id"`
					Name        string `json:"name"`
					Description string `json:"description"`
					ShortText   string `json:"short_text"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				return ops.CreatePackage(cfg.Exe, ops.PackageRequest{ID: a.PackageID, Name: a.Name, Description: a.Description, ShortText: a.ShortText})
			},
		},
		{
			Name: "upload_artifact", Title: "Upload artifact from a local directory",
			Description: "Create or update a designtime artifact from a local directory (action CREATED, UPDATED or UNCHANGED; SKIPPED with skipped \"draft\" when the tenant's artifact is in draft: someone edits it in the Web UI, it is not overwritten). The package must exist (create_package); the directory needs META-INF/MANIFEST.MF and src/main/resources. Does not deploy: next validate_artifact, then deploy.",
			InputSchema: object(props{
				"artifact_id": str("Artifact ID (must match Bundle-SymbolicName)"),
				"name":        str("Display name, defaults to Bundle-Name of the manifest, else artifact_id"),
				"type":        enum("Artifact type", cpi.ArtifactTypes...),
				"package_id":  str("Integration package ID (must exist)"),
				"dir":         str("Local artifact directory, relative to the server root"),
				"dry_run":     boolean("Only compare with the tenant and report what the upload would do (action, version); nothing is written"),
			}, "artifact_id", "type", "package_id", "dir"),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					ArtifactID string `json:"artifact_id"`
					Name       string `json:"name"`
					Type       string `json:"type"`
					PackageID  string `json:"package_id"`
					Dir        string `json:"dir"`
					DryRun     bool   `json:"dry_run"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				dir, err := resolvePath(cfg.Root, a.Dir)
				if err != nil {
					return nil, err
				}
				return ops.UploadArtifact(cfg.Exe, ops.UploadRequest{ID: a.ArtifactID, Name: a.Name, Type: a.Type, PackageID: a.PackageID, Dir: dir, Versioning: cfg.Versioning, DryRun: a.DryRun, SkipDraft: true})
			},
		},
		{
			Name: "upload_artifacts", Title: "Upload several artifacts",
			Description: "upload_artifact for several artifacts in one call (in parallel): one result per artifact (action CREATED, UPDATED or UNCHANGED, or error). The packages must exist. dry_run only compares. Does not deploy.",
			InputSchema: object(props{
				"artifacts": map[string]any{"type": "array", "description": "Artifacts to upload", "items": object(props{
					"artifact_id": str("Artifact ID (must match Bundle-SymbolicName)"),
					"name":        str("Display name, defaults to Bundle-Name of the manifest, else artifact_id"),
					"type":        enum("Artifact type", cpi.ArtifactTypes...),
					"package_id":  str("Integration package ID (must exist)"),
					"dir":         str("Local artifact directory, relative to the server root"),
				}, "artifact_id", "type", "package_id", "dir")},
				"dry_run": boolean("Only compare with the tenant and report what the uploads would do; nothing is written"),
			}, "artifacts"),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Artifacts []struct {
						ArtifactID string `json:"artifact_id"`
						Name       string `json:"name"`
						Type       string `json:"type"`
						PackageID  string `json:"package_id"`
						Dir        string `json:"dir"`
					} `json:"artifacts"`
					DryRun bool `json:"dry_run"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if len(a.Artifacts) == 0 {
					return nil, output.Usagef("artifacts is required")
				}
				reqs := make([]ops.UploadRequest, len(a.Artifacts))
				for i, it := range a.Artifacts {
					dir, err := resolvePath(cfg.Root, it.Dir)
					if err != nil {
						return nil, err
					}
					reqs[i] = ops.UploadRequest{ID: it.ArtifactID, Name: it.Name, Type: it.Type, PackageID: it.PackageID, Dir: dir,
						Versioning: cfg.Versioning, DryRun: a.DryRun, SkipDraft: true}
				}
				type entry struct {
					*ops.UploadResult
					ID    string `json:"id"`
					Error string `json:"error,omitempty"`
				}
				entries := make([]entry, len(reqs))
				failed := forEachParallel(len(reqs), batchParallel, func(i int) error {
					res, err := ops.UploadArtifact(cfg.Exe, reqs[i])
					entries[i] = entry{UploadResult: res, ID: reqs[i].ID}
					if err != nil {
						entries[i].Error = err.Error()
					}
					return err
				})
				return map[string]any{"results": entries}, batchErr(failed, len(reqs))
			},
		},
		{
			Name: "deploy", Title: "Deploy artifacts",
			Description: "Deploy designtime artifacts to runtime and wait for the outcome. One result per artifact: DEPLOYED, SKIPPED (skipped \"draft\": in draft on the tenant, not deployed), FAILED (with the tenant's error message) or TIMEOUT. A redeploy is only DEPLOYED once the runtime shows the new deployment. A designtime version older than the running one is refused (FAILED) unless the designtime artifact was changed after the running deployment (rule 'modified after deployment') or allow_downgrade; each result's rule says which rule decided. Next: send_test_message to test the flow.",
			InputSchema: object(props{
				"artifact_ids":          strArray("Artifact IDs"),
				"artifact_type":         enum(`Artifact type, default "Integration"`, cpi.ArtifactTypes...),
				"compare_versions":      boolean("Skip artifacts whose version is already running (default false: always deploy)"),
				"allow_downgrade":       boolean("Deploy even if the designtime version is older than the running one (default false: such a deploy FAILS before it is triggered, because the tenant copy was probably not updated)"),
				"poll_interval_seconds": integer("Seconds between status checks"),
				"max_checks":            integer("Maximum number of status checks per artifact"),
				"dry_run":               boolean("Only predict per artifact whether it would be deployed and why (designtime vs. running version and status); nothing is triggered"),
			}, "artifact_ids"),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					ArtifactIDs     []string `json:"artifact_ids"`
					ArtifactType    string   `json:"artifact_type"`
					CompareVersions bool     `json:"compare_versions"`
					AllowDowngrade  bool     `json:"allow_downgrade"`
					PollInterval    *int     `json:"poll_interval_seconds"`
					MaxChecks       *int     `json:"max_checks"`
					DryRun          bool     `json:"dry_run"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				ids, err := requireIDs(a.ArtifactIDs)
				if err != nil {
					return nil, err
				}
				if a.ArtifactType == "" {
					a.ArtifactType = "Integration"
				}
				if !cpi.IsValidArtifactType(a.ArtifactType) {
					return nil, output.Usagef("invalid artifact_type %q (valid: %s)", a.ArtifactType, strings.Join(cpi.ArtifactTypes, ", "))
				}
				if a.DryRun {
					plan, err := ops.PlanDeploy(cfg.Exe, a.ArtifactType, ids, cfg.Versioning, a.AllowDowngrade)
					return map[string]any{"plan": plan}, err
				}
				artifacts := make([]ops.Artifact, 0, len(ids))
				for _, id := range ids {
					artifacts = append(artifacts, ops.Artifact{ID: id, Type: a.ArtifactType})
				}
				opts := pollOptions(cfg, a.PollInterval, a.MaxChecks)
				opts.CompareVersions = a.CompareVersions
				opts.AllowDowngrade = a.AllowDowngrade
				opts.Versioning = cfg.Versioning
				results := ops.Deploy(ctx, tenant, artifacts, opts)
				return map[string]any{"results": results}, ops.Err(results)
			},
		},
		{
			Name: "send_test_message", Title: "Send a test message",
			Description: "Send a test message to an endpoint of a deployed integration flow (only URLs the tenant lists for it) and return the HTTP status, the response and the message GUID. " +
				"Flows started by ProcessDirect are reached through the test harness flow with process_direct_address. " +
				"With wait_seconds it also waits for the message processing log and returns it (status, error text, custom headers, attachments). " +
				"This triggers real processing, including calls to receivers: use test data on a development tenant. On failure: get_message_steps with the message GUID.",
			InputSchema: object(props{
				"artifact_id":  str("Integration flow ID (deployed)"),
				"url":          str("Endpoint URL from list_service_endpoints; only needed when the flow has several endpoints"),
				"method":       enum(`HTTP method, default "POST"`, "POST", "PUT", "PATCH", "GET", "DELETE"),
				"body":         str("Message body (text)"),
				"content_type": str("Content-Type of the body, e.g. application/xml"),
				"headers":      map[string]any{"type": "object", "description": "Additional HTTP headers (not Authorization, Cookie or X-CSRF-Token)", "additionalProperties": map[string]any{"type": "string"}},
				"wait_seconds": map[string]any{"type": "integer", "minimum": 0, "maximum": 600, "description": "Wait up to this long for the message processing log to reach a final status"},
				"max_bytes":    maxBytesSchema(),
				"trace":        boolean("Send a W3C traceparent header and return its traceId for get_trace_tree (default true; a traceparent in headers is kept)"),
				"process_direct_address": str("For flows with a ProcessDirect sender: send through the test harness flow to this address (e.g. /billing/in); " +
					"artifact_id is then the flow behind the address and the returned log is that flow's message"),
				"harness": str(`Test harness flow ID, default "` + ops.DefaultHarnessID + `"`),
			}, "artifact_id"),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": true},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					ArtifactID  string            `json:"artifact_id"`
					URL         string            `json:"url"`
					Method      string            `json:"method"`
					Body        string            `json:"body"`
					ContentType string            `json:"content_type"`
					Headers     map[string]string `json:"headers"`
					WaitSeconds int               `json:"wait_seconds"`
					MaxBytes    int               `json:"max_bytes"`
					Address     string            `json:"process_direct_address"`
					Trace       *bool             `json:"trace"`
					Harness     string            `json:"harness"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if err := checkMaxBytes(a.MaxBytes); err != nil {
					return nil, err
				}
				if a.WaitSeconds < 0 || a.WaitSeconds > 600 {
					return nil, output.Usagef("wait_seconds must be between 0 and 600")
				}
				if endpoints == nil {
					return nil, output.Usagef("sending test messages is not configured for this server")
				}
				return ops.SendTestMessage(ctx, cfg.Exe, endpoints, ops.TestMessage{
					ArtifactID: a.ArtifactID, URL: a.URL, Method: a.Method, Body: []byte(a.Body), ContentType: a.ContentType,
					Headers: a.Headers, MaxBytes: a.MaxBytes, Wait: time.Duration(a.WaitSeconds) * time.Second, PollInterval: cfg.LogPollInterval,
					ProcessDirectAddress: a.Address, Harness: a.Harness, NoTrace: a.Trace != nil && !*a.Trace,
				})
			},
		},
		{
			Name: "undeploy", Title: "Undeploy artifacts",
			Description: "Remove artifacts from runtime and wait until they are gone (status UNDEPLOYED, NOT_DEPLOYED, FAILED or TIMEOUT). Designtime artifacts are kept. Requires confirm=true.",
			InputSchema: object(props{
				"artifact_ids":          strArray("Artifact IDs"),
				"confirm":               boolean("Must be true: stops the integration on the tenant"),
				"poll_interval_seconds": integer("Seconds between status checks"),
				"max_checks":            integer("Maximum number of status checks per artifact"),
			}, "artifact_ids", "confirm"),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": true, "openWorldHint": true},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					ArtifactIDs  []string `json:"artifact_ids"`
					Confirm      bool     `json:"confirm"`
					PollInterval *int     `json:"poll_interval_seconds"`
					MaxChecks    *int     `json:"max_checks"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				ids, err := requireIDs(a.ArtifactIDs)
				if err != nil {
					return nil, err
				}
				if !a.Confirm {
					return nil, output.Usagef("undeploy stops %s on the tenant; call again with confirm=true", strings.Join(ids, ", "))
				}
				artifacts := make([]ops.Artifact, 0, len(ids))
				for _, id := range ids {
					artifacts = append(artifacts, ops.Artifact{ID: id})
				}
				results := ops.Undeploy(ctx, tenant, artifacts, pollOptions(cfg, a.PollInterval, a.MaxChecks))
				return map[string]any{"results": results}, ops.Err(results)
			},
		},
		{
			Name: "pd_deploy", Title: "Deploy Partner Directory parameters",
			Description: "Upload Partner Directory parameters from a local directory ({PID}/String.properties, {PID}/Binary/). Runs as a dry run unless dry_run=false. " +
				"keys=[\"PID:ID\"] deploys only those parameters (create or update, nothing else is touched): use it to fix one mapping. " +
				"full_sync deletes remote parameters of the managed PIDs that do not exist locally; check with pd_diff first.",
			InputSchema: object(props{
				"resources_path": str("Local partner directory root, relative to the server root"),
				"pids":           strArray("Restrict to these partner IDs"),
				"replace":        boolean("Update existing parameters whose value differs (default true)"),
				"full_sync":      boolean("Delete remote parameters not present locally (default false)"),
				"dry_run":        boolean("Only report what would change (default true)"),
				"keys":           strArray("Only these parameters, \"PID:ID\" (not with full_sync or pids)"),
			}, "resources_path"),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": true, "openWorldHint": true},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					ResourcesPath string   `json:"resources_path"`
					PIDs          []string `json:"pids"`
					Replace       *bool    `json:"replace"`
					FullSync      bool     `json:"full_sync"`
					DryRun        *bool    `json:"dry_run"`
					Keys          []string `json:"keys"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				dir, err := resolvePath(cfg.Root, a.ResourcesPath)
				if err != nil {
					return nil, err
				}
				if a.FullSync && cfg.DenyFullSync {
					return nil, output.Usagef("full_sync is not allowed in this mode: deploy single parameters with keys, or ask the user to run 'cpictl pd-deploy --full-sync'")
				}
				opts := ops.PDDeployOptions{Replace: true, FullSync: a.FullSync, DryRun: true, PIDs: a.PIDs, Keys: a.Keys}
				if a.Replace != nil {
					opts.Replace = *a.Replace
				}
				if a.DryRun != nil {
					opts.DryRun = *a.DryRun
				}
				return ops.PDDeploy(cpi.NewPartnerDirectory(cfg.Exe), repo.NewPartnerDirectory(dir), opts)
			},
		},
		{
			Name: "get_pd_parameters", Title: "Read Partner Directory parameters",
			Description: "String and binary Partner Directory parameters of one partner ID on the tenant (all, or only keys). " +
				"Binaries show content type, size and sha256; their content only with include_content. Use it before pd_deploy to see the tenant's value.",
			InputSchema: object(props{
				"pid":             str("Partner ID"),
				"keys":            strArray("Only these parameter IDs"),
				"include_content": boolean("Return binary content (text inline, else base64; default false)"),
				"max_bytes":       maxBytesSchema(),
			}, "pid"),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Pid            string   `json:"pid"`
					Keys           []string `json:"keys"`
					IncludeContent bool     `json:"include_content"`
					MaxBytes       int      `json:"max_bytes"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if err := checkMaxBytes(a.MaxBytes); err != nil {
					return nil, err
				}
				return ops.GetPDParameters(cpi.NewPartnerDirectory(cfg.Exe), a.Pid, a.Keys, a.IncludeContent, a.MaxBytes)
			},
		},
		{
			Name: "pd_diff", Title: "Compare local Partner Directory files with the tenant",
			Description: "Per parameter: create, update (value, content or content type), unchanged, or remote_only (exists only on the tenant: pd_deploy full_sync would delete it). " +
				"Binaries are compared by content hash and content type. A PID whose local files cannot be read is reported as an error, never as empty.",
			InputSchema: object(props{
				"resources_path": str("Local partner directory root, relative to the server root"),
				"pids":           strArray("Only these partner IDs"),
			}, "resources_path"),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					ResourcesPath string   `json:"resources_path"`
					PIDs          []string `json:"pids"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				dir, err := resolvePath(cfg.Root, a.ResourcesPath)
				if err != nil {
					return nil, err
				}
				return ops.PDDiff(cpi.NewPartnerDirectory(cfg.Exe), repo.NewPartnerDirectory(dir), a.PIDs)
			},
		},
		{
			Name: "pd_dependencies", Title: "Find which flows use Partner Directory parameters",
			Description: "Scan local content (inside the server root) for Partner Directory references: pd:<PID>:<ID>:<Binary|String> in .iflw models (with the step id), dynamic pd:${...} references, " +
				"and getParameter(id, pid, ...) in Groovy scripts. With resources_path, unknownPids lists referenced PIDs that have no local directory (often a typo in the model). " +
				"Use it before pd_deploy: every listed flow is affected by the change. Reads local files only.",
			InputSchema: object(props{
				"local_dir":      str("Local content directory, relative to the server root"),
				"resources_path": str("Local Partner Directory tree, to report unknown PIDs"),
				"pid":            str("Only references to this partner ID"),
				"id":             str("Only references to this parameter ID"),
			}, "local_dir"),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					LocalDir      string `json:"local_dir"`
					ResourcesPath string `json:"resources_path"`
					Pid           string `json:"pid"`
					ID            string `json:"id"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				dir, err := resolvePath(cfg.Root, a.LocalDir)
				if err != nil {
					return nil, err
				}
				f := ops.PDDependenciesFilter{Pid: a.Pid, ID: a.ID}
				if a.ResourcesPath != "" {
					if f.ResourcesPath, err = resolvePath(cfg.Root, a.ResourcesPath); err != nil {
						return nil, err
					}
				}
				return ops.FindPDDependencies(ctx, dir, f)
			},
		},
		{
			Name: "config_diff", Title: "Compare a configure file with the tenant",
			Description: "Compare the parameters of a configure YAML file (or folder) with the tenant: per key update (with local and tenant value), unchanged, or unknown_key. " +
				"Shows what 'cpictl configure' would write and which artifacts it would redeploy (only those with a change).",
			InputSchema: object(props{
				"config_path":       str("Configure YAML file or folder, relative to the server root"),
				"package_filter":    strArray("Only these packages (IDs as in the file)"),
				"artifact_filter":   strArray("Only these artifacts (IDs as in the file)"),
				"deployment_prefix": str("Prefix for package and artifact IDs (overrides the file's deploymentPrefix)"),
			}, "config_path"),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					ConfigPath       string   `json:"config_path"`
					PackageFilter    []string `json:"package_filter"`
					ArtifactFilter   []string `json:"artifact_filter"`
					DeploymentPrefix string   `json:"deployment_prefix"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				path, err := resolvePath(cfg.Root, a.ConfigPath)
				if err != nil {
					return nil, err
				}
				files, err := ops.LoadConfigureFiles(path)
				if err != nil {
					return nil, output.Usage(err)
				}
				return ops.ConfigDiff(cfg.Exe, ops.MergeConfigureFiles(files, a.DeploymentPrefix), ops.ConfigFilter{Packages: a.PackageFilter, Artifacts: a.ArtifactFilter})
			},
		},
		{
			Name: "drift", Title: "Compare local artifacts with the tenant",
			Description: "For each artifact of a local content tree (optionally one package folder): local Bundle-Version and content vs the designtime version and content on the tenant, and the runtime version. " +
				"State in_sync, tenant_newer (someone edited on the tenant: download before you upload, or their change is lost), local_newer, diverged, not_on_tenant; runtimeOutdated when the deployed version differs. " +
				"Run it before upload_artifact. Read-only; downloads each artifact.",
			InputSchema: object(props{
				"local_dir":  str("Local content directory, relative to the server root"),
				"package_id": str("Only artifacts in this package folder"),
			}, "local_dir"),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					LocalDir  string `json:"local_dir"`
					PackageID string `json:"package_id"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				dir, err := resolvePath(cfg.Root, a.LocalDir)
				if err != nil {
					return nil, err
				}
				return ops.Drift(ctx, cfg.Exe, dir, a.PackageID, 0)
			},
		},
		{
			Name: "compare", Title: "Compare two sides per artifact",
			Description: "Compare two sides per artifact: content (as an upload compares it), version, parameters.prop keys and files. A side is \"tenant\" (this server's tenant, downloaded and normalized as snapshot writes it), " +
				"a content tree inside the server root, or \"git:<ref>[:<path>]\" of the repository at the root. Status per artifact: same, only_a, only_b, content_differs, version_differs, parameters_differ; " +
				"for the tenant the designtime and running version, draft flag and who changed it last. diff adds unified diffs, show_values parameter values. Read-only; downloads the tenant's artifacts (limit with packages / artifacts).",
			InputSchema: object(props{
				"a":           str(`Side A: "tenant", a directory relative to the root, or "git:<ref>[:<path>]"`),
				"b":           str("Side B, same forms"),
				"packages":    strArray("Only these packages (IDs or patterns)"),
				"artifacts":   strArray("Only these artifacts (IDs or patterns)"),
				"diff":        boolean("Unified diffs of changed text files"),
				"show_values": boolean("Parameter values (default: only which keys differ)"),
			}, "a", "b"),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					A          string   `json:"a"`
					B          string   `json:"b"`
					Packages   []string `json:"packages"`
					Artifacts  []string `json:"artifacts"`
					Diff       bool     `json:"diff"`
					ShowValues bool     `json:"show_values"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				work, err := os.MkdirTemp("", "cpictl-compare-*")
				if err != nil {
					return nil, err
				}
				defer os.RemoveAll(work)
				sides := make([]ops.CompareSide, 2)
				for i, spec := range []string{a.A, a.B} {
					switch {
					case spec == "":
						return nil, output.Usagef("sides a and b are required")
					case spec == "tenant":
						sides[i] = ops.CompareSide{Label: "tenant", Exe: cfg.Exe}
					case strings.HasPrefix(spec, "git:"):
						ref, sub, _ := strings.Cut(strings.TrimPrefix(spec, "git:"), ":")
						root, err := resolvePath(cfg.Root, ".")
						if err != nil {
							return nil, err
						}
						dir := filepath.Join(work, fmt.Sprintf("git%d", i))
						if err := ops.GitTree(ctx, root, ref, sub, dir); err != nil {
							return nil, err
						}
						sides[i] = ops.CompareSide{Label: spec, Dir: dir}
					default:
						dir, err := resolvePath(cfg.Root, spec)
						if err != nil {
							return nil, err
						}
						sides[i] = ops.CompareSide{Label: spec, Dir: dir}
					}
				}
				return ops.Compare(ctx, sides[0], sides[1], ops.CompareOptions{Packages: a.Packages, Artifacts: a.Artifacts,
					Diff: a.Diff, Values: a.ShowValues, WorkDir: filepath.Join(work, "tenant")})
			},
		},
		{
			Name: "transport_check", Title: "Check a transport against this tenant",
			Description: "Before moving artifacts to this server's tenant (the target tier): their dependencies (script collections and mappings they reference, flows they call, credentials and key aliases, Partner Directory parameters) " +
				"and pre-checks against the tenant: draft on the target, changes made outside the pipeline (with target_dir, the target's content in Git), dependencies and called flows present, credentials and key aliases (also when a parameter names them), expiring certificates, Partner Directory parameters, " +
				"and the flows' configuration (missing on the target, the source value travels, the same environment-specific value on both). Each check pass / warn / fail / skip; allow_missing turns failures for material someone else provides into warnings. Read-only. Run it before uploading to a higher tier; with_deps adds referenced script collections and mappings.",
			InputSchema: object(props{
				"artifacts":     strArray("Artifact IDs or folder names to move"),
				"dir":           str("Source content tree relative to the root (default: packages if it exists, else the root)"),
				"with_deps":     boolean("Add the script collections and mappings the artifacts reference"),
				"target_dir":    str(`The target tier's content in Git: a directory relative to the root or "git:<ref>[:<path>]"`),
				"configure":     str("The target tier's configure file or folder, relative to the root"),
				"allow_missing": strArray("Checks whose failures become warnings because someone else provides the material on the target: credential, keystore, pd, parameters, dependency"),
				"expiry_days":   integer("Warn about certificates and keys expiring within this many days on the target (default 30)"),
			}, "artifacts"),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Artifacts    []string `json:"artifacts"`
					Dir          string   `json:"dir"`
					WithDeps     bool     `json:"with_deps"`
					TargetDir    string   `json:"target_dir"`
					Configure    string   `json:"configure"`
					AllowMissing []string `json:"allow_missing"`
					ExpiryDays   int      `json:"expiry_days"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				o, err := mcpLintOptions(cfg.Root, a.Dir, nil, nil)
				if err != nil {
					return nil, err
				}
				topts := ops.TransportCheckOptions{WithDeps: a.WithDeps, AllowMissing: a.AllowMissing, ExpiryDays: a.ExpiryDays}
				if a.TargetDir != "" {
					if strings.HasPrefix(a.TargetDir, "git:") {
						ref, sub, _ := strings.Cut(strings.TrimPrefix(a.TargetDir, "git:"), ":")
						root, err := resolvePath(cfg.Root, ".")
						if err != nil {
							return nil, err
						}
						work, err := os.MkdirTemp("", "cpictl-transport-*")
						if err != nil {
							return nil, err
						}
						defer os.RemoveAll(work)
						if err := ops.GitTree(ctx, root, ref, sub, work); err != nil {
							return nil, err
						}
						topts.TargetDir = work
					} else if topts.TargetDir, err = resolvePath(cfg.Root, a.TargetDir); err != nil {
						return nil, err
					}
				}
				if a.Configure != "" {
					p, err := resolvePath(cfg.Root, a.Configure)
					if err != nil {
						return nil, err
					}
					files, err := ops.LoadConfigureFiles(p)
					if err != nil {
						return nil, output.Usage(err)
					}
					topts.Configure = ops.MergeConfigureFiles(files, "")
				}
				return ops.CheckTransport(ctx, cfg.Exe, o.Dir, a.Artifacts, topts)
			},
		},
		{
			Name: "discover_tenant", Title: "Discover conventions of existing content",
			Description: "Inventory packages and integration flows (adapters, steps, exception subprocesses, log levels, scripts, parameters, credential names, naming patterns) " +
				"and write it as JSON to output_file inside the server root, plus graph.json next to it (query it with graph_search, graph_neighbors, graph_path); returns the summary. Read-only on the tenant. " +
				"Use it once to derive the conventions of a tenant; it downloads every flow, so limit with package_ids on large tenants. local_dir analyses a local repository instead.",
			InputSchema: object(props{
				"package_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Only these packages (default: all)"},
				"max_iflows":  map[string]any{"type": "integer", "minimum": 0, "description": "Stop after this many integration flows (default: all)"},
				"output_file": str(`JSON file relative to the server root, default ".cpi/discovery.json"`),
				"local_dir":   str("Analyse this local directory (relative to the server root) instead of the tenant"),
			}),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					PackageIDs []string `json:"package_ids"`
					MaxIFlows  int      `json:"max_iflows"`
					OutputFile string   `json:"output_file"`
					LocalDir   string   `json:"local_dir"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if a.OutputFile == "" {
					a.OutputFile = ".cpi/discovery.json"
				}
				out, err := resolvePath(cfg.Root, a.OutputFile)
				if err != nil {
					return nil, err
				}
				var d *ops.Discovery
				if a.LocalDir != "" {
					dir, err := resolvePath(cfg.Root, a.LocalDir)
					if err != nil {
						return nil, err
					}
					d, err = ops.DiscoverDir(ctx, dir)
					if err != nil {
						return nil, err
					}
					d.Source = a.LocalDir
				} else {
					d, err = ops.DiscoverTenant(ctx, cfg.Exe, cfg.TenantHost, ops.DiscoverOptions{PackageIDs: a.PackageIDs, MaxIFlows: a.MaxIFlows})
					if err != nil {
						return nil, err
					}
				}
				if err := ops.WriteDiscovery(d, out); err != nil {
					return nil, err
				}
				g := ops.BuildGraph(d)
				if err := ops.WriteGraph(g, ops.GraphFile(out)); err != nil {
					return nil, err
				}
				return map[string]any{"file": a.OutputFile, "graphFile": filepath.ToSlash(ops.GraphFile(a.OutputFile)), "graph": g.Stats,
					"source": d.Source, "summary": d.Summary, "errors": d.Errors}, nil
			},
		},
	}
}

// cachedEndpointExecuters reuses one executer per endpoint URL, so OAuth and
// CSRF tokens survive between test messages.
func cachedEndpointExecuters(newExe ops.EndpointExecuterFunc) ops.EndpointExecuterFunc {
	if newExe == nil {
		return nil
	}
	type entry struct {
		exe  *httpclnt.HTTPExecuter
		path string
	}
	var mu sync.Mutex
	cache := map[string]entry{}
	return func(url string) (*httpclnt.HTTPExecuter, string, error) {
		mu.Lock()
		defer mu.Unlock()
		if e, ok := cache[url]; ok {
			return e.exe, e.path, nil
		}
		exe, path, err := newExe(url)
		if err != nil {
			return nil, "", err
		}
		cache[url] = entry{exe, path}
		return exe, path, nil
	}
}

func pollOptions(cfg Config, interval, maxChecks *int) ops.Options {
	opts := ops.Options{Interval: cfg.PollInterval, MaxChecks: cfg.MaxChecks}
	if interval != nil && *interval >= 0 {
		opts.Interval = time.Duration(*interval) * time.Second
	}
	if maxChecks != nil && *maxChecks > 0 {
		opts.MaxChecks = *maxChecks
	}
	return opts
}

// decode strictly decodes tool arguments; unknown fields are usage errors so
// that a misspelt argument is never silently ignored.
func decode(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return output.Usagef("invalid arguments: %v", err)
	}
	return nil
}

func requireIDs(ids []string) ([]string, error) {
	var out []string
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		return nil, output.Usagef("artifact_ids must contain at least one ID")
	}
	return out, nil
}

// resolvePath resolves p (relative paths against root) and rejects paths
// outside root.
func resolvePath(root, p string) (string, error) {
	if p == "" {
		return "", output.Usagef("path is required")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target := p
	if !filepath.IsAbs(target) {
		target = filepath.Join(absRoot, target)
	}
	target = filepath.Clean(target)
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}
	if resolvedRoot, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = resolvedRoot
	}
	rel, err := filepath.Rel(absRoot, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", output.Usagef("path %q is outside the server root %q", p, absRoot)
	}
	return target, nil
}

type props = map[string]any

func object(properties props, required ...string) map[string]any {
	if properties == nil {
		properties = props{}
	}
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func boolean(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}
func integer(desc string) map[string]any {
	return map[string]any{"type": "integer", "minimum": 0, "description": desc}
}
func strArray(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1, "description": desc}
}
func enum(desc string, values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values, "description": desc}
}

func maxBytesSchema() map[string]any {
	return map[string]any{"type": "integer", "minimum": 1, "maximum": 1048576, "description": "Maximum bytes returned, default 65536"}
}

func mustAbs(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

func checkMaxBytes(n int) error {
	if n < 0 || n > 1048576 {
		return output.Usagef("max_bytes must be between 1 and 1048576")
	}
	return nil
}

// mcpLintOptions resolves the lint paths inside the server root.
func mcpLintOptions(root, dir string, packages, artifacts []string) (lint.Options, error) {
	if dir == "" {
		dir = "."
		if info, err := os.Stat(filepath.Join(root, "packages")); err == nil && info.IsDir() {
			dir = "packages"
		}
	}
	abs, err := resolvePath(root, dir)
	if err != nil {
		return lint.Options{}, err
	}
	base, err := resolvePath(root, ".")
	if err != nil {
		return lint.Options{}, err
	}
	cfg, err := lint.LoadConfig(filepath.Join(base, ".cpi", "lint.yaml"))
	if err != nil {
		return lint.Options{}, err
	}
	o := lint.Options{Dir: abs, Packages: packages, Artifacts: artifacts, Config: cfg,
		Baseline: filepath.Join(base, ".cpi", "lint-baseline.json")}
	if c, err := catalog.Load(filepath.Join(base, filepath.FromSlash(ops.ComponentsFile))); err == nil {
		o.Components = c
	} else if !errors.Is(err, fs.ErrNotExist) {
		return o, err
	}
	return o, nil
}
