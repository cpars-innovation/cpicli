package mcp

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/stats"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type rpcResp struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

// session sends the messages to a fresh server and returns the responses by id.
func session(t *testing.T, mock *cpitest.Tenant, root string, msgs ...string) map[string]rpcResp {
	t.Helper()
	return sessionWith(t, Config{Exe: mock.Executer(), Root: root, PollInterval: time.Millisecond, MaxChecks: 3}, msgs...)
}

func sessionWith(t *testing.T, cfg Config, msgs ...string) map[string]rpcResp {
	t.Helper()
	return serve(t, NewServer("cpicli", "test", Instructions, Tools(cfg)), msgs...)
}

func serve(t *testing.T, srv *Server, msgs ...string) map[string]rpcResp {
	t.Helper()
	var out bytes.Buffer
	require.NoError(t, srv.Serve(context.Background(), strings.NewReader(strings.Join(msgs, "\n")+"\n"), &out))

	byID := map[string]rpcResp{}
	for line := range strings.SplitSeq(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var r rpcResp
		require.NoError(t, json.Unmarshal([]byte(line), &r), "every stdout line must be a JSON-RPC message: %q", line)
		byID[string(r.ID)] = r
	}
	return byID
}

func call(id int, tool string, args any) string {
	a, _ := json.Marshal(args)
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, id, tool, a)
}

type toolCallResult struct {
	IsError           bool       `json:"isError"`
	StructuredContent ToolResult `json:"structuredContent"`
	Content           []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func toolResult(t *testing.T, r rpcResp) toolCallResult {
	t.Helper()
	require.Nil(t, r.Error, "unexpected JSON-RPC error")
	var res toolCallResult
	require.NoError(t, json.Unmarshal(r.Result, &res))
	require.Len(t, res.Content, 1)
	assert.True(t, json.Valid([]byte(res.Content[0].Text)))
	return res
}

func TestProtocol(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	resp := session(t, mock, t.TempDir(),
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":4,"method":"bogus"}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"nope","arguments":{}}}`,
		`not json`,
	)
	require.Len(t, resp, 6, "the notification gets no response")

	var init struct {
		ProtocolVersion string                `json:"protocolVersion"`
		ServerInfo      struct{ Name string } `json:"serverInfo"`
		Capabilities    map[string]any        `json:"capabilities"`
	}
	require.NoError(t, json.Unmarshal(resp["1"].Result, &init))
	assert.Equal(t, "2025-03-26", init.ProtocolVersion)
	assert.Equal(t, "cpicli", init.ServerInfo.Name)
	assert.Contains(t, init.Capabilities, "tools")

	var list struct {
		Tools []struct {
			Name        string         `json:"name"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(resp["2"].Result, &list))
	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
		assert.Equal(t, "object", tool.InputSchema["type"], tool.Name)
	}
	assert.Equal(t, []string{"doctor", "list_packages", "list_artifacts", "list_message_logs", "get_message_log",
		"set_log_level", "get_message_trace", "message_summary", "get_trace_tree",
		"list_runtime_artifacts", "list_service_endpoints",
		"validate_artifact", "check_guidelines", "list_resources", "get_resource", "download_artifact", "copy_iflow", "bump_versions", "lint", "lint_fix", "layout_iflow",
		"list_credentials", "list_keystore",
		"get_parameters", "set_parameters", "create_package", "upload_artifacts", "deploy", "send_test_message", "idoc_template", "undeploy", "pd_deploy",
		"get_pd_parameters", "pd_diff", "pd_dependencies", "config_diff", "drift", "compare", "transport_check", "discover_tenant",
		"list_data_stores", "list_data_store_entries", "get_data_store_entry", "delete_data_store_entry", "list_variables", "list_jms_queues", "get_jms_broker", "list_number_ranges", "list_log_files", "get_log_file", "list_idempotent_entries", "list_id_mappings", "list_locks", "list_runtimes", "unlock_artifact", "list_components", "list_integration_connections", "get_tenant_settings", "download_log_archive", "list_value_mappings", "get_value_mapping", "set_value_mapping", "delete_artifact", "delete_package",
		"list_jms_messages", "retry_jms_messages", "move_jms_messages", "delete_jms_messages", "set_jms_queue_state", "find_messages_by_header",
		"list_keystore_history", "get_certificate_chain", "get_csr", "get_ssh_public_key", "generate_key_pair", "upload_certificate_chain", "delete_keystore_entry",
		"list_pgp_keys", "list_access_policies", "create_access_policy", "add_artifact_reference", "delete_access_policy", "delete_artifact_reference",
		"list_audit_logs", "list_pd_partners", "list_alternative_partners", "create_alternative_partner", "delete_alternative_partner",
		"list_authorized_users", "create_authorized_user", "delete_authorized_user", "graph_search", "graph_neighbors", "graph_path",
		"install_test_tools", "set_stub_response", "list_tests", "run_tests", "run_script_cases", "run_script_cases_online", "get_message_content"}, names)

	assert.Nil(t, resp["3"].Error)
	assert.Equal(t, codeMethodNotFound, resp["4"].Error.Code)
	assert.Equal(t, codeInvalidParams, resp["5"].Error.Code)
	assert.Equal(t, codeParseError, resp["null"].Error.Code)
}

func TestUnknownProtocolVersionGetsLatest(t *testing.T) {
	resp := session(t, cpitest.NewTenant(t, nil), t.TempDir(),
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`)
	assert.Contains(t, string(resp["1"].Result), SupportedProtocolVersions[0])
}

func TestDeployAndStatusTools(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"A": {Type: "Integration", DesignVersion: "1.0.1", TaskStatuses: []string{"SUCCESS"},
			AfterDeploy: []*cpitest.Runtime{{Version: "1.0.1", Status: "STARTED", DeployedOn: time.Now()}}},
		"B": {Type: "Integration", DesignVersion: "1", TaskStatuses: []string{"FAIL"}, ErrorInfo: "script error"},
	})
	resp := session(t, mock, t.TempDir(),
		call(1, "deploy", map[string]any{"artifact_ids": []string{"A", "B"}}),
	)
	res := toolResult(t, resp["1"])
	assert.True(t, res.IsError)
	assert.Equal(t, "partial", res.StructuredContent.ErrorCategory)
	assert.Equal(t, 7, res.StructuredContent.ExitCode)
	assert.Contains(t, res.Content[0].Text, `"durationMs":`, "every result reports its duration")
	results := res.StructuredContent.Result.(map[string]any)["results"].([]any)
	assert.Equal(t, "DEPLOYED", results[0].(map[string]any)["status"])
	assert.Equal(t, "FAILED", results[1].(map[string]any)["status"])
	assert.Contains(t, results[1].(map[string]any)["error"], "script error")

	resp = session(t, mock, t.TempDir(), call(2, "list_runtime_artifacts", map[string]any{"artifact_ids": []string{"A"}}))
	res = toolResult(t, resp["2"])
	assert.False(t, res.IsError)
	assert.True(t, res.StructuredContent.OK)
}

func TestToolArgumentErrorsAreUsage(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"A": {Runtime: &cpitest.Runtime{Status: "STARTED"}}})
	root := t.TempDir()
	resp := session(t, mock, root,
		call(1, "undeploy", map[string]any{"artifact_ids": []string{"A"}, "confirm": false}),
		call(2, "deploy", map[string]any{"artifact_ids": []string{"A"}, "artifactIds": []string{"typo"}}),
		call(3, "deploy", map[string]any{"artifact_ids": []string{}}),
		call(4, "upload_artifacts", map[string]any{"artifacts": []map[string]any{{"artifact_id": "A", "type": "Integration", "package_id": "P", "dir": "../../etc"}}}),
		call(5, "deploy", map[string]any{"artifact_ids": []string{"A"}, "artifact_type": "Bogus"}),
	)
	for id := 1; id <= 5; id++ {
		res := toolResult(t, resp[fmt.Sprint(id)])
		assert.True(t, res.IsError, "call %d", id)
		assert.Equal(t, "usage", res.StructuredContent.ErrorCategory, "call %d: %s", id, res.StructuredContent.Error)
	}
	assert.Zero(t, mock.Count("DELETE "), "undeploy without confirm must not touch the tenant")
	assert.Zero(t, mock.Count("POST "))
}

func TestUndeployWithConfirm(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"A": {Runtime: &cpitest.Runtime{Status: "STARTED"}, UndeployAfter: 1}})
	resp := session(t, mock, t.TempDir(), call(1, "undeploy", map[string]any{"artifact_ids": []string{"A"}, "confirm": true}))
	res := toolResult(t, resp["1"])
	assert.True(t, res.StructuredContent.OK, res.StructuredContent.Error)
	assert.Equal(t, 1, mock.Count("DELETE "))
}

func TestParameterTools(t *testing.T) {
	a := &cpitest.Artifact{Type: "Integration", DesignVersion: "1", Parameters: map[string]string{"Host": "old"}}
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"A": a})
	resp := session(t, mock, t.TempDir(),
		call(1, "set_parameters", map[string]any{"artifact_id": "A", "parameters": map[string]string{"Host": "new"}}),
	)
	res := toolResult(t, resp["1"])
	assert.True(t, res.StructuredContent.OK, res.StructuredContent.Error)
	assert.Equal(t, "new", a.Parameters["Host"])

	resp = session(t, mock, t.TempDir(), call(2, "get_parameters", map[string]any{"artifact_id": "A"}))
	assert.Contains(t, toolResult(t, resp["2"]).Content[0].Text, `"value":"new"`)
}

func TestPDDeployDefaultsToDryRun(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pd", "P1"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "pd", "P1", "String.properties"), []byte("K=V\n"), 0644))
	mock := cpitest.NewTenant(t, nil)
	resp := session(t, mock, root, call(1, "pd_deploy", map[string]any{"resources_path": "pd", "full_sync": true}))
	res := toolResult(t, resp["1"])
	assert.Equal(t, true, res.StructuredContent.Result.(map[string]any)["dryRun"])
	assert.Zero(t, mock.Count("POST "))
	assert.Zero(t, mock.Count("PUT "))
	assert.Zero(t, mock.Count("DELETE "))
}

func TestResolvePath(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "a"), 0755))
	p, err := resolvePath(root, "a")
	require.NoError(t, err)
	assert.Equal(t, "a", filepath.Base(p))
	for _, bad := range []string{"..", "../x", "/etc", "a/../../x"} {
		_, err := resolvePath(root, bad)
		assert.Error(t, err, bad)
	}
	// a symlink pointing outside the root is rejected too
	outside := t.TempDir()
	cpitest.Symlink(t, outside, filepath.Join(root, "link"))
	_, err = resolvePath(root, "link")
	assert.Error(t, err)
	_, err = resolvePath(root, filepath.Join("link", "new.txt"))
	assert.Error(t, err, "a new file below it too")
	// and one that points nowhere: writing through it would create its target
	cpitest.Symlink(t, filepath.Join(outside, "missing"), filepath.Join(root, "dangling"))
	_, err = resolvePath(root, "dangling")
	assert.Error(t, err)
}

func TestMessageLogTools(t *testing.T) {
	now := time.Now()
	mock := cpitest.NewTenant(t, nil)
	mock.MessageLogSteps = [][]cpitest.MessageLog{
		{{Guid: "g1", Artifact: "A", Status: "PROCESSING", Start: now}},
		{{Guid: "g1", Artifact: "A", Status: "FAILED", Start: now, End: now, ErrorText: "Mapping failed at line 3",
			Headers: map[string]string{"OrderId": "4711"}}},
	}
	srv := NewServer("cpicli", "test", Instructions, Tools(Config{Exe: mock.Executer(), Root: t.TempDir(), LogPollInterval: time.Millisecond}))
	var out bytes.Buffer
	in := strings.Join([]string{
		call(1, "list_message_logs", map[string]any{"artifact_id": "A", "since": "5m", "wait_seconds": 5}),
	}, "\n") + "\n"
	require.NoError(t, srv.Serve(context.Background(), strings.NewReader(in), &out))
	var r rpcResp
	require.NoError(t, json.Unmarshal(out.Bytes(), &r))
	res := toolResult(t, r)
	assert.True(t, res.StructuredContent.OK, res.StructuredContent.Error)
	logs := res.StructuredContent.Result.(map[string]any)["logs"].([]any)
	assert.Equal(t, "FAILED", logs[0].(map[string]any)["status"])
	assert.Equal(t, "Mapping failed at line 3", logs[0].(map[string]any)["errorText"], "errors are included by default")

	resp := session(t, mock, t.TempDir(),
		call(2, "get_message_log", map[string]any{"message_guid": "g1"}),
		call(3, "list_message_logs", map[string]any{"statuses": []string{"BROKEN"}}),
		call(4, "list_message_logs", map[string]any{"since": "yesterday-ish"}),
	)
	detail := toolResult(t, resp["2"])
	assert.True(t, detail.StructuredContent.OK)
	assert.Contains(t, detail.Content[0].Text, `"OrderId"`)
	assert.Equal(t, "usage", toolResult(t, resp["3"]).StructuredContent.ErrorCategory)
	assert.Equal(t, "usage", toolResult(t, resp["4"]).StructuredContent.ErrorCategory)
}

func TestContentTools(t *testing.T) {
	now := time.Now()
	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	w, _ := zw.Create("META-INF/MANIFEST.MF")
	_, _ = w.Write([]byte("Manifest-Version: 1.0\n"))
	require.NoError(t, zw.Close())

	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"A": {Type: "Integration", DesignVersion: "1", EndpointURL: "https://tenant/http/a", Zip: zipBuf.Bytes(),
			ValidationResult: "Check execution result: Failed - receiver missing",
			Runtime:          &cpitest.Runtime{Version: "1", Status: "ERROR"}, ErrorInfo: "boom",
			Resources: map[string]cpitest.Resource{"s.groovy": {Type: "groovy", Content: []byte("x=1")}}},
	})
	mock.MessageLogSteps = [][]cpitest.MessageLog{{{Guid: "g1", Artifact: "A", Status: "FAILED", Start: now, End: now,
		Attachments: map[string]string{"in.xml": "<in/>"}, StoreEntries: map[string]string{"e1": "<persisted/>"},
		Steps: []cpitest.Step{{StepID: "s1", ModelStepID: "Mapping_1", Status: "FAILED", Error: "bad"}}}}}
	root := t.TempDir()

	resp := session(t, mock, root,
		call(1, "validate_artifact", map[string]any{"artifact_id": "A"}),
		call(2, "list_service_endpoints", map[string]any{"artifact_id": "A"}),
		call(3, "list_runtime_artifacts", map[string]any{"statuses": []string{"ERROR"}}),
		call(4, "get_resource", map[string]any{"artifact_id": "A", "name": "s.groovy", "type": "groovy"}),
		call(5, "download_artifact", map[string]any{"artifact_id": "A", "dir": "work/A"}),
		call(6, "download_artifact", map[string]any{"artifact_id": "A", "dir": "../outside"}),
		call(7, "get_message_log", map[string]any{"message_guid": "g1", "include_steps": true}),
		call(8, "get_message_content", map[string]any{"attachment_id": "att-g1-in.xml"}),
		call(9, "get_message_content", map[string]any{"store_entry_id": "e1", "max_bytes": 5}),
		call(10, "get_resource", map[string]any{"artifact_id": "A", "name": "s.groovy", "type": "groovy", "max_bytes": 99999999}),
	)
	v := toolResult(t, resp["1"])
	assert.Equal(t, "failed", v.StructuredContent.ErrorCategory)
	assert.Contains(t, v.Content[0].Text, "receiver missing")
	assert.Contains(t, toolResult(t, resp["2"]).Content[0].Text, "https://tenant/http/a")
	assert.Contains(t, toolResult(t, resp["3"]).Content[0].Text, `"errorInfo":"boom"`)
	assert.Contains(t, toolResult(t, resp["4"]).Content[0].Text, `"text":"x=1"`)
	assert.True(t, toolResult(t, resp["5"]).StructuredContent.OK)
	assert.FileExists(t, filepath.Join(root, "work", "A", "META-INF", "MANIFEST.MF"))
	assert.Equal(t, "usage", toolResult(t, resp["6"]).StructuredContent.ErrorCategory)
	assert.Contains(t, toolResult(t, resp["7"]).Content[0].Text, `"modelStepId":"Mapping_1"`)
	assert.Contains(t, toolResult(t, resp["8"]).Content[0].Text, `"text":"<in/>"`, "XML stays readable (no HTML escaping)")
	entry := toolResult(t, resp["9"]).Content[0].Text
	assert.Contains(t, entry, `"truncated":true`)
	assert.Equal(t, "usage", toolResult(t, resp["10"]).StructuredContent.ErrorCategory)
}

func TestSecurityToolsAreReadOnlyAndSecretFree(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	mock.Credentials = map[string]map[string]map[string]any{
		"UserCredentials": {"ERP": {"Name": "ERP", "User": "svc", "Password": "topsecret"}},
	}
	mock.Keystore = []cpitest.KeystoreEntry{{Alias: "partner", NotAfter: time.Now().Add(5 * 24 * time.Hour)}}
	resp := session(t, mock, t.TempDir(),
		call(1, "list_credentials", map[string]any{}),
		call(2, "list_keystore", map[string]any{"expiring_within_days": 30}),
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
	)
	creds := toolResult(t, resp["1"]).Content[0].Text
	assert.Contains(t, creds, `"user":"svc"`)
	assert.NotContains(t, creds, "topsecret")
	assert.Contains(t, toolResult(t, resp["2"]).Content[0].Text, `"expiringSoon":true`)
	assert.NotContains(t, string(resp["3"].Result), "set_credential", "no tool writes security material")
	for _, r := range mock.Requests() {
		assert.True(t, strings.HasPrefix(r, "GET "), "security tools only read: %s", r)
	}
}

// Tool calls are recorded in the local usage statistics (source mcp).
func TestToolCallsAreRecorded(t *testing.T) {
	file := filepath.Join(t.TempDir(), "stats.jsonl")
	t.Setenv("CPICTL_STATS_FILE", file)
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{})
	session(t, mock, t.TempDir(), call(1, "list_packages", map[string]any{}))
	entries, err := stats.Read(file, time.Time{})
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "list_packages", entries[0].Command)
	assert.Equal(t, stats.SourceMCP, entries[0].Source)
}

// deploy with dry_run predicts and triggers nothing.
func TestDeployDryRun(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"Same":  {Type: "Integration", DesignVersion: "1.0.0", Runtime: &cpitest.Runtime{Version: "1.0.0", Status: "STARTED"}},
		"Newer": {Type: "Integration", DesignVersion: "1.0.1", Runtime: &cpitest.Runtime{Version: "1.0.0", Status: "STARTED"}},
		"Idle":  {Type: "Integration", DesignVersion: "1.0.0"},
	})
	resp := session(t, mock, t.TempDir(), call(1, "deploy", map[string]any{"artifact_ids": []string{"Same", "Newer", "Idle"}, "dry_run": true}))
	res := toolResult(t, resp["1"])
	require.False(t, res.IsError, res.Content[0].Text)
	plan := res.StructuredContent.Result.(map[string]any)["plan"].([]any)
	require.Len(t, plan, 3)
	assert.Equal(t, false, plan[0].(map[string]any)["deploy"])
	assert.Equal(t, true, plan[1].(map[string]any)["deploy"])
	assert.Equal(t, "not deployed yet", plan[2].(map[string]any)["reason"])
	assert.Equal(t, 0, mock.Count("POST "), "nothing triggered")
}

// list_packages / list_artifacts are cached; refresh and tenant changes
// read again.
func TestListCache(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"A": {Type: "Integration", DesignVersion: "1.0.0", Package: "P", Name: "A"},
	})
	mock.Packages = []cpitest.Package{{ID: "P", Version: "1.0.0"}}
	cfg := Config{Exe: mock.Executer(), Root: t.TempDir(), PollInterval: time.Millisecond, MaxChecks: 3, CacheTTL: time.Minute}
	srv := NewServer("cpicli", "test", Instructions, Tools(cfg))
	lists := func() int { return mock.Count("GET /api/v1/IntegrationPackages") }

	// concurrent identical calls share one tenant request
	resp := serve(t, srv, call(1, "list_packages", map[string]any{}), call(2, "list_packages", map[string]any{}))
	assert.Equal(t, 1, lists())
	cached := 0
	for _, id := range []string{"1", "2"} {
		if strings.Contains(toolResult(t, resp[id]).Content[0].Text, `"cached":true`) {
			cached++
		}
	}
	assert.Equal(t, 1, cached)

	serve(t, srv, call(1, "list_packages", map[string]any{}))
	assert.Equal(t, 1, lists(), "from the cache")
	serve(t, srv, call(1, "list_packages", map[string]any{"refresh": true}))
	assert.Equal(t, 2, lists(), "refresh reads again")

	// a tenant-changing tool clears the cache
	serve(t, srv, call(1, "create_package", map[string]any{"package_id": "Q"}))
	before := lists()
	serve(t, srv, call(1, "list_packages", map[string]any{}))
	assert.Equal(t, before+1, lists())

	// ttl 0: no cache
	cfg.CacheTTL = 0
	srv = NewServer("cpicli", "test", Instructions, Tools(cfg))
	before = lists()
	serve(t, srv, call(1, "list_packages", map[string]any{}))
	serve(t, srv, call(1, "list_packages", map[string]any{}))
	assert.Equal(t, before+2, lists())
}

// get_parameters artifact_ids reads several flows; one unknown flow makes the
// call partial.
func TestGetParametersBatch(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"A": {Type: "Integration", DesignVersion: "1.0.0", Parameters: map[string]string{"Host": "a"}},
		"B": {Type: "Integration", DesignVersion: "1.0.0", Parameters: map[string]string{"Host": "b"}},
	})
	resp := session(t, mock, t.TempDir(), call(1, "get_parameters", map[string]any{"artifact_ids": []string{"A", "B", "Nope"}}))
	res := toolResult(t, resp["1"])
	assert.Equal(t, "partial", res.StructuredContent.ErrorCategory)
	text := res.Content[0].Text
	assert.Contains(t, text, `"artifactId":"A"`)
	assert.Contains(t, text, `"b"`)
	assert.Contains(t, text, `"artifactId":"Nope","error"`)

	resp = session(t, mock, t.TempDir(), call(1, "get_parameters", map[string]any{"artifact_id": "A", "artifact_ids": []string{"B"}}))
	assert.Equal(t, "usage", toolResult(t, resp["1"]).StructuredContent.ErrorCategory)
}

// upload_artifacts uploads several artifacts; a bad item makes it partial.
func TestUploadArtifactsBatch(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"A": {Type: "Integration", Package: "P"},
		"B": {Type: "Integration", Package: "P"},
	})
	mock.Packages = []cpitest.Package{{ID: "P"}}
	root := t.TempDir()
	for _, id := range []string{"A", "B"} {
		for name, content := range map[string]string{
			"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\nBundle-SymbolicName: " + id + "\nBundle-Version: 1.0.0\n",
			"src/main/resources/scenarioflows/integrationflow/" + id + ".iflw": "<bpmn2:definitions/>",
		} {
			p := filepath.Join(root, id, filepath.FromSlash(name))
			require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
			require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
		}
	}
	item := func(id, dir string) map[string]any {
		return map[string]any{"artifact_id": id, "type": "Integration", "package_id": "P", "dir": dir}
	}
	resp := session(t, mock, root, call(1, "upload_artifacts", map[string]any{"artifacts": []any{item("A", "A"), item("B", "B"), item("C", "missing")}}))
	res := toolResult(t, resp["1"])
	assert.Equal(t, "partial", res.StructuredContent.ErrorCategory, res.Content[0].Text)
	assert.Equal(t, 1, mock.Artifacts["A"].Uploads)
	assert.Equal(t, 1, mock.Artifacts["B"].Uploads)
	assert.Contains(t, res.Content[0].Text, `"action":"CREATED"`)
	assert.Contains(t, res.Content[0].Text, `"id":"C","error"`)
}

// lint and lint_fix work on the local files below the server root.
func TestLintTools(t *testing.T) {
	root := t.TempDir()
	model := `<?xml version="1.0"?><bpmn2:definitions xmlns:bpmn2="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:ifl="http:///com.sap.ifl.model/Ifl.xsd">
<bpmn2:process id="P"><bpmn2:startEvent id="Start"/><bpmn2:callActivity id="S" name="Log"><bpmn2:extensionElements>
<ifl:property><key>activityType</key><value>Script</value></ifl:property><ifl:property><key>script</key><value>Log.groovy</value></ifl:property>
</bpmn2:extensionElements></bpmn2:callActivity><bpmn2:sequenceFlow id="F" sourceRef="Start" targetRef="S"/></bpmn2:process></bpmn2:definitions>`
	for _, id := range []string{"A", "B"} {
		dir := filepath.Join(root, "packages", "Pkg", id)
		for name, content := range map[string]string{
			"META-INF/MANIFEST.MF": "Bundle-SymbolicName: " + id + "\nSAP-BundleType: IntegrationFlow\n",
			"src/main/resources/scenarioflows/integrationflow/" + id + ".iflw": model,
			"src/main/resources/script/Log.groovy":                             "return message\n",
		} {
			p := filepath.Join(dir, filepath.FromSlash(name))
			require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
			require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
		}
	}
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{})
	resp := session(t, mock, root,
		call(1, "lint", map[string]any{"rules": []string{"duplicate-script"}}),
		call(2, "lint_fix", map[string]any{"dry_run": true}),
		call(3, "lint", map[string]any{"dir": "../outside"}),
	)
	res := toolResult(t, resp["1"])
	require.False(t, res.IsError, res.Content[0].Text)
	assert.Equal(t, 2, strings.Count(res.Content[0].Text, `"rule":"duplicate-script"`))
	assert.NotContains(t, res.Content[0].Text, `"rule":"no-exception-subprocess"`, "rules filter")
	res = toolResult(t, resp["2"])
	require.False(t, res.IsError, res.Content[0].Text)
	assert.Contains(t, res.Content[0].Text, `"Pkg/Pkg_Scripts"`)
	assert.FileExists(t, filepath.Join(root, "packages", "Pkg", "A", "src", "main", "resources", "script", "Log.groovy"), "dry run")
	assert.Equal(t, "usage", toolResult(t, resp["3"]).StructuredContent.ErrorCategory, "paths stay inside the root")
}

func TestLayoutTool(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "packages", "Pkg", "A")
	require.NoError(t, os.CopyFS(dir, os.DirFS(filepath.Join("..", "..", "test", "testdata", "artifacts", "collection", "IFlow1"))))
	model := filepath.Join(dir, "src", "main", "resources", "scenarioflows", "integrationflow", "IFlow1.iflw")
	before, err := os.ReadFile(model)
	require.NoError(t, err)
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{})
	resp := session(t, mock, root,
		call(1, "layout_iflow", map[string]any{"paths": []string{"packages/Pkg/A"}, "check": true}),
		call(2, "layout_iflow", map[string]any{"paths": []string{"packages/Pkg"}, "dry_run": true}),
		call(3, "layout_iflow", map[string]any{"paths": []string{"packages"}, "mode": "full"}),
		call(4, "layout_iflow", map[string]any{"paths": []string{"../outside"}}),
	)
	for _, id := range []string{"1", "2", "3"} {
		res := toolResult(t, resp[id])
		require.False(t, res.IsError, res.Content[0].Text)
		assert.Contains(t, res.Content[0].Text, `"path":"packages/Pkg/A/src/main/resources/scenarioflows/integrationflow/IFlow1.iflw"`, "relative to the root")
	}
	assert.Contains(t, toolResult(t, resp["3"]).Content[0].Text, `"changed":1`)
	after, _ := os.ReadFile(model)
	assert.NotEqual(t, string(before), string(after), "laid out")
	assert.Equal(t, "usage", toolResult(t, resp["4"]).StructuredContent.ErrorCategory, "paths stay inside the root")
}

func TestCompareTool(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"META-INF/MANIFEST.MF":               "Manifest-Version: 1.0\nBundle-SymbolicName: A\nBundle-Version: 1.0.1\nSAP-BundleType: IntegrationFlow\n",
		"src/main/resources/script/s.groovy": "local\n",
		"src/main/resources/scenarioflows/integrationflow/A.iflw": `<bpmn2:definitions xmlns:bpmn2="http://www.omg.org/spec/BPMN/20100524/MODEL">` +
			`<bpmn2:process id="Process_1"><bpmn2:callActivity id="CallActivity_1" name="local"/></bpmn2:process></bpmn2:definitions>`,
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		p := filepath.Join(root, "packages", "P", "A", filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
		w, _ := zw.Create(name)
		if name == "src/main/resources/script/s.groovy" {
			content = "tenant\n"
		}
		content = strings.Replace(content, `name="local"`, `name="tenant"`, 1)
		_, _ = w.Write([]byte(content))
	}
	require.NoError(t, zw.Close())
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"A": {Type: "Integration", DesignVersion: "1.0.1", Package: "P", Name: "A", Zip: buf.Bytes()}})
	mock.Packages = []cpitest.Package{{ID: "P", Version: "1.0.0"}}
	resp := session(t, mock, root,
		call(1, "compare", map[string]any{"a": "packages", "b": "tenant", "diff": true}),
		call(2, "compare", map[string]any{"a": "../outside", "b": "tenant"}),
		call(3, "compare", map[string]any{"a": "packages", "b": "tenant", "model": true}),
	)
	res := toolResult(t, resp["1"])
	require.False(t, res.IsError, res.Content[0].Text)
	assert.Contains(t, res.Content[0].Text, `"status":"content_differs"`)
	assert.Contains(t, res.Content[0].Text, `+tenant`)
	assert.Equal(t, "usage", toolResult(t, resp["2"]).StructuredContent.ErrorCategory)
	res = toolResult(t, resp["3"])
	require.False(t, res.IsError, res.Content[0].Text)
	assert.Contains(t, res.Content[0].Text, `"model":{"elements":[{"id":"CallActivity_1","type":"callActivity","kind":"callActivity","name":"tenant","change":"changed","renamed":["local","tenant"]}]}`)
}

// One over-long line is refused with a parse error; the session goes on.
func TestServeRefusesOversizedRequest(t *testing.T) {
	defer func(n int) { MaxRequestBytes = n }(MaxRequestBytes)
	MaxRequestBytes = 64
	in := strings.Repeat("x", 200) + "\n" + `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n"
	var out bytes.Buffer
	s := NewServer("t", "1", "", nil)
	require.NoError(t, s.Serve(context.Background(), strings.NewReader(in), &out))
	assert.Contains(t, out.String(), "request larger than 64 bytes")
	assert.Contains(t, out.String(), `"id":1`, "the next request is still answered")
}

// A path that does not exist yet resolves like an existing one when the
// root's path goes through a symbolic link (macOS /var, Windows 8.3 names).
func TestResolvePathNewFileBelowLinkedRoot(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "root")
	cpitest.Symlink(t, real, link)
	got, err := resolvePath(link, filepath.Join("new", "file.txt"))
	require.NoError(t, err)
	want, err := filepath.EvalSymlinks(real)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(want, "new", "file.txt"), got)
	_, err = resolvePath(link, filepath.Join("..", "outside"))
	assert.Error(t, err)
}
