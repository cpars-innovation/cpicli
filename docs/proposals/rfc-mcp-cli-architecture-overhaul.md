# RFC 001: MCP & CLI Architecture Overhaul, Tool Consolidation, and Subcommand Hierarchy

- **Status**: Proposed
- **Author**: Antigravity / Engineering Review
- **Date**: 2026-10-18
- **Target**: `cpictl` (v0.8.0 / v1.0.0 Architecture Evolution)

---

## Executive Summary

`cpictl` has rapidly expanded from an SAP Cloud Integration deployment tool into a versatile developer, CI/CD, and AI agent platform. However, rapid feature addition has introduced architectural friction across both user and machine interfaces:

1. **MCP Tool Proliferation (59 Tools Registered)**:
   The MCP server registers 59 distinct tools into the model context by default. This consumes **15,000–25,000 tokens on every prompt**, degrades LLM tool selection accuracy, increases multi-step call latency, and causes tool hallucination. Redundant tools (e.g. `upload_artifact` vs `upload_artifacts`, 13 separate store tools, 9 fragmented message log tools) exacerbate token wastage.
2. **CLI Command Sprawl (47 Top-Level Commands)**:
   The CLI has over 45 flat, top-level commands directly registered on `rootCmd`. Commands from identical domains are scattered (e.g. `pd`, `pd-snapshot`, `pd-deploy` are three disconnected top-level commands; runtime stores like `datastore`, `variables`, `jms`, `numberranges`, `logfiles`, `idempotent`, `idmappings` clutter the root namespace).
3. **Cross-Platform Bugs & Security Traversal Flaws**:
   Code and test audits revealed serious Windows path resolution vulnerabilities in `resolvePath` (treating `/etc` as relative), test suite failures on non-POSIX platforms (`os.UserHomeDir()` ignoring `HOME` on Windows, symlink privilege errors, and hardcoded POSIX file permission assertions).

This RFC proposes a comprehensive architectural restructuring:
- **Consolidating 59 MCP tools into ~22 unified, resource-oriented tools** with progressive disclosure via dynamic toolsets.
- **Refactoring 47 top-level CLI commands into 9 cohesive namespaces** (`artifact`, `package`, `deploy`, `logs`, `store`, `pd`, `security`, `config`, `ops`) with backwards-compatible legacy aliases.
- **Fixing critical security and cross-platform defects** identified during test execution.

---

## 1. Problem Analysis & Audit Findings

### 1.1 The MCP Tool Bloat Problem

Exposing 59 distinct tools to an AI model poses significant challenges:
- **Context Window Inefficiency**: In modern agent frameworks (Claude Code, Cursor, OpenCode, Codex), full JSON schemas for 59 tools consume between 15,000 and 25,000 tokens. This directly penalizes developer cost and context limit.
- **Latency & Round Trips**: To diagnose a single failed message, an agent currently must execute 4 to 5 round trips:
  1. `list_message_logs` (find message GUID)
  2. `get_message_log` (inspect status and error)
  3. `get_message_steps` (find failed step and `modelStepId`)
  4. `get_message_attachment` (download logged payload)
  5. `get_message_trace` (if trace was active)
- **Tool Redundancy & Cognitive Friction**:
  - `upload_artifact` vs `upload_artifacts`: One takes a single object, the other takes an array. (Contrast with `deploy`, which accepts an array `artifact_ids` uniformly).
  - `lint` vs `lint_fix`: Two separate tools where `lint` with an optional `fix: bool` is more idiomatic.
  - 13 separate store tools: `list_data_stores`, `list_data_store_entries`, `get_data_store_entry`, `delete_data_store_entry`, `list_variables`, `get_variable`, `list_jms_queues`, `get_jms_broker`, `list_number_ranges`, `list_log_files`, `get_log_file`, `list_idempotent_entries`, `list_id_mappings`.
  - 4 separate diff tools: `drift`, `compare`, `config_diff`, `pd_diff`.

### 1.2 The CLI Command Flatness & Naming Inconsistency

When running `cpictl --help`, users face a monolithic list of 47 commands without logical hierarchy.

| Issue | Current Implementation | Impact |
|---|---|---|
| **Partner Directory Fragmentation** | `pd` (has `get`, `diff`, `deps`), `pd-snapshot` (root), `pd-deploy` (root) | Inconsistent ergonomics: users must memorize which operations are subcommands of `pd` and which are top-level kebab-case commands. |
| **Runtime Storage Scatter** | `datastore`, `variables`, `jms`, `numberranges`, `logfiles`, `idempotent`, `idmappings` all on `rootCmd` | Root command space is polluted by 7 separate niche store commands. |
| **Observability Fragmentation** | `logs` (has 8 subcommands), `loglevel` (root), `logfiles` (root) | Users looking to manage logs find three disparate top-level commands. |
| **Security Credential Scatter** | `credentials` (root), `keystore` (root) | Not grouped under a security namespace. |
| **Package & Artifact Lifecycle Confusion** | `packages`, `artifacts`, `update package`, `update artifact`, `download`, `params`, `endpoints`, `resources` | Creation is under `packages create`, update is under `update package`, inspection is under `artifacts` and `packages`. |

### 1.3 Critical Defects & Cross-Platform Issues Discovered

During execution of the test suite and source analysis, several critical issues were identified:

1. **Windows Path Traversal Bypass in `resolvePath` (`internal/mcp/tools.go:1795`)**:
   ```go
   target := p
   if !filepath.IsAbs(target) {
       target = filepath.Join(absRoot, target)
   }
   ```
   *Bug*: On Windows, paths like `/etc` or `\temp` have `filepath.IsAbs(p) == false` because they lack a drive letter (e.g., `C:`). `filepath.Join(absRoot, "/etc")` appends `/etc` to `absRoot`, treating Unix absolute paths or root-relative paths as valid local subpaths and bypassing boundary validation.
2. **Test Suite Failure on Windows (`HOME` vs `USERPROFILE`)**:
   - `profile_test.go`, `projectconfig_test.go`, `compare_test.go`, `credentials_test.go` set `t.Setenv("HOME", home)`.
   - Go's `os.UserHomeDir()` on Windows ignores `HOME` and reads `USERPROFILE`. As a result, tests look in the developer's actual machine directory (`C:\Users\<user>\.cpictl`), causing multiple test failures.
3. **Unchecked Symlink Privileges in Tests**:
   - `TestResolvePath` (`internal/mcp/server_test.go`) and `TestFullSyncAbortsOnLocalReadError` (`pkg/ops/pd_test.go`) call `os.Symlink` without verifying OS privileges. On Windows without Developer Mode, this raises `A required privilege is not held by the client`.
4. **POSIX Permission Assertions on NTFS**:
   - `internal/stats/stats_test.go:35` asserts exact `0600` mode. On Windows NTFS, Go creates files with `0666`, causing deterministic test failure.
5. **Flaky Concurrency Assertion**:
   - `orchestrator_baseline_test.go:247` asserts `< 1.2s` for parallel uploads, failing under heavy test runner load (`1.49s`).

---

## 2. MCP Tool Consolidation Proposal

We propose reducing the 59 MCP tools down to **22 consolidated tools**, grouped by capability.

### 2.1 Proposed Tool Map

| # | Current Tools (59) | Proposed Consolidated Tool (22) | Consolidation Strategy & Rationale |
|---|---|---|---|
| 1 | `upload_artifact`, `upload_artifacts` | `upload_artifact` | Polymorphic: accept either a single artifact object or an array of artifacts. Eliminates redundant tool definitions. |
| 2 | `deploy` | `deploy` | Retain; already accepts array of IDs with `dry_run` support. |
| 3 | `undeploy` | `undeploy` | Retain; accepts array of IDs with confirmation. |
| 4 | `get_runtime_status`, `list_runtime_artifacts`, `list_service_endpoints` | `runtime_status` | Unified query: inspect deployed status, endpoints, and runtime errors in one call. |
| 5 | `list_message_logs`, `message_summary` | `query_messages` | Query message processing logs with optional summary/aggregation mode. |
| 6 | `get_message_log`, `get_message_steps`, `get_message_attachment`, `get_message_store_entry` | `get_message_details` | Unified inspection: `include: ["steps", "attachments", "store_entries", "trace"]`. Eliminates 4 round trips during debugging. |
| 7 | `get_message_trace`, `get_trace_message`, `get_trace_tree` | `trace_message` | Cross-flow and step trace investigation in a single tool. |
| 8 | `set_log_level` | `set_log_level` | Retain; controls temporary log level escalation. |
| 9 | `send_test_message` | `send_test_message` | Retain; sends message and returns execution result. |
| 10 | `validate_artifact`, `check_guidelines` | `validate_artifact` | Unified tenant checks: mode `["syntax", "guidelines", "all"]`. |
| 11 | `lint`, `lint_fix` | `lint` | Unify: `fix: true` applies fixes; `fix: false` reports findings. |
| 12 | `layout_iflow` | `layout_iflow` | Retain; diagram layout engine. |
| 13 | `list_packages`, `create_package` | `manage_package` | Actions: `list`, `create`. |
| 14 | `list_artifacts`, `download_artifact`, `copy_iflow` | `manage_artifact` | Actions: `list`, `download`, `copy`. |
| 15 | `list_resources`, `get_resource` | `manage_resources` | Actions: `list`, `get`. |
| 16 | `get_parameters`, `set_parameters` | `configure_parameters` | Unified parameter management: read or update parameters. |
| 17 | `list_data_stores`, `list_data_store_entries`, `get_data_store_entry`, `delete_data_store_entry` | `manage_data_store` | Actions: `list_stores`, `list_entries`, `get_entry`, `delete_entry`. Reduces 4 tools to 1. |
| 18 | `list_variables`, `get_variable`, `list_number_ranges`, `list_idempotent_entries`, `list_id_mappings` | `runtime_storage` | Generic key-value and runtime store query (`type: "variable" \| "number_range" \| "idempotent" \| "id_mapping"`). |
| 19 | `list_jms_queues`, `get_jms_broker` | `jms_status` | Combines queue metrics and broker capacity. |
| 20 | `list_log_files`, `get_log_file` | `manage_log_files` | Actions: `list`, `get_tail`. |
| 21 | `pd_deploy`, `get_pd_parameters`, `pd_diff`, `pd_dependencies` | `partner_directory` | Actions: `get`, `diff`, `deps`, `deploy`. Unifies all Partner Directory operations. |
| 22 | `drift`, `compare`, `config_diff`, `transport_check` | `diff_content` | Unified diffing tool: target `["artifact", "partner_directory", "config", "transport"]`. |

*Note on utility tools*: `doctor`, `help`, `list_toolsets`, `enable_toolset`, and `loop_*` remain available via dynamic discovery or standard utility sets.

### 2.2 Progressive Disclosure via Dynamic Toolsets by Default

Instead of exposing all tools unconditionally:
1. **Core Set (Default, ~10 tools)**:
   - `doctor`, `help`, `runtime_status`, `query_messages`, `get_message_details`, `upload_artifact`, `deploy`, `send_test_message`, `manage_artifact`, `lint`.
2. **Specialized Toolsets (Loaded dynamically on demand)**:
   - `stores`: `manage_data_store`, `runtime_storage`, `jms_status`, `manage_log_files`.
   - `pd`: `partner_directory`.
   - `diff`: `diff_content`.
   - `security`: `list_credentials`, `list_keystore`.

---

## 3. CLI Command Hierarchy Redesign

We propose organizing the CLI into **9 intuitive command namespaces**, mirroring modern cloud CLIs (`kubectl`, `gh`, `aws`), with full backwards compatibility.

### 3.1 Proposed Command Tree

```text
cpictl
├── artifact                 # Artifact lifecycle & inspection (replaces artifacts, iflow)
│   ├── list                 # List artifacts of package (alias: cpictl artifacts)
│   ├── get / download       # Download artifact zip or tree (alias: cpictl download)
│   ├── upload               # Upload from directory (alias: cpictl update artifact)
│   ├── validate             # Tenant designtime validation (alias: cpictl validate)
│   ├── guidelines           # Design guidelines check (alias: cpictl guidelines)
│   ├── lint                 # Local lint checks & auto-fixes (alias: cpictl lint)
│   ├── layout               # Format flow diagram layout (alias: cpictl iflow layout)
│   ├── copy                 # Clone/copy flow (alias: cpictl iflow copy)
│   ├── resources            # List/read resources (alias: cpictl resources)
│   └── params               # Read/write parameters (alias: cpictl params)
│
├── package                  # Integration package operations (replaces packages)
│   ├── list                 # List packages (alias: cpictl packages)
│   ├── create               # Create package (alias: cpictl packages create)
│   └── update               # Update package metadata (alias: cpictl update package)
│
├── deploy                   # Deployment & runtime lifecycle
│   ├── apply / run          # Deploy artifacts (alias: cpictl deploy)
│   ├── remove               # Undeploy artifacts (alias: cpictl undeploy)
│   ├── status               # Deployed status & errors (alias: cpictl status)
│   ├── endpoints            # Show service URLs (alias: cpictl endpoints)
│   ├── matrix               # Multi-tenant deployment matrix (alias: cpictl matrix)
│   └── orchestrator         # Automated pipeline deploy (alias: cpictl orchestrator)
│
├── logs                     # Observability & diagnostic logs
│   ├── query / list         # Query MPLs (default: cpictl logs)
│   ├── get                  # Get message log
│   ├── steps                # Inspect message step runs
│   ├── attachment           # Download attachment
│   ├── payload              # Download persisted message
│   ├── trace                # Query trace messages
│   ├── tree                 # Cross-flow trace tree
│   ├── summary              # Aggregated failure summary
│   ├── level                # Change runtime log level (alias: cpictl loglevel)
│   └── files                # Inspect system & HTTP logs (alias: cpictl logfiles)
│
├── store                    # Tenant runtime storage (consolidates 7 root commands)
│   ├── data                 # Data stores: list, entries, get, delete (alias: cpictl datastore)
│   ├── variable             # Flow & global variables (alias: cpictl variables)
│   ├── jms                  # JMS queues & broker metrics (alias: cpictl jms)
│   ├── number-range         # Number range objects (alias: cpictl numberranges)
│   ├── idempotent           # Idempotent repository entries (alias: cpictl idempotent)
│   └── id-mapping           # ID mapper entries (alias: cpictl idmappings)
│
├── pd                       # Partner Directory (consolidates pd, pd-snapshot, pd-deploy)
│   ├── get                  # Inspect partner parameters
│   ├── diff                 # Diff local vs remote parameters
│   ├── deps                 # Analyze flow dependencies
│   ├── deploy               # Upload local PD parameters (alias: cpictl pd-deploy)
│   └── snapshot             # Download PD parameters (alias: cpictl pd-snapshot)
│
├── security                 # Credentials & Keystore
│   ├── credentials          # List security credentials (alias: cpictl credentials)
│   └── keystore             # List certificates & keys (alias: cpictl keystore)
│
├── config                   # Configuration & Profiles
│   ├── profile              # Manage tenant profiles (alias: cpictl profile)
│   ├── generate             # Generate config files (alias: cpictl config-generate)
│   └── pull                 # Pull runtime config (alias: cpictl configure)
│
└── ops                      # Testing & System diagnostics
    ├── send                 # Send test message to flow (alias: cpictl send)
    ├── doctor               # Diagnose connection & roles (alias: cpictl doctor)
    ├── stats                # Tool usage statistics (alias: cpictl stats)
    ├── mocktenant           # Embedded mock tenant engine (alias: cpictl mocktenant)
    └── mcp                  # Run Model Context Protocol server (alias: cpictl mcp)
```

### 3.2 Backwards Compatibility & Graceful Migration

To prevent breaking existing CI/CD pipelines, GitHub Actions, and user shell scripts:
1. All legacy top-level commands (e.g. `cpictl pd-deploy`, `cpictl datastore`, `cpictl loglevel`) remain fully functional as hidden aliases.
2. When executed, deprecated aliases output a subtle notice to `stderr`:
   `Deprecation Notice: 'cpictl pd-deploy' is now 'cpictl pd deploy'. The alias will remain supported.`
3. Flag structures and JSON output contracts remain 100% identical.

---

## 4. Implementation & Bug Fix Plan

### 4.1 Security Fix: Safe Path Resolution
Refactor `resolvePath` in `internal/mcp/tools.go` to be strictly multiplatform:
```go
func resolvePath(root, p string) (string, error) {
    if p == "" {
        return "", output.Usagef("path is required")
    }
    absRoot, err := filepath.Abs(root)
    if err != nil {
        return "", err
    }
    // Reject Windows root-relative or drive-less volume paths
    if filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "\\") {
        // Must verify absolute path falls cleanly within absRoot
    }
    cleanPath := filepath.Clean(filepath.Join(absRoot, p))
    rel, err := filepath.Rel(absRoot, cleanPath)
    if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
        return "", output.Usagef("path %q is outside the server root %q", p, absRoot)
    }
    return cleanPath, nil
}
```

### 4.2 Cross-Platform Test Harness Fix: UserHomeDir
Add `cpitest.MockHome(t, tempDir)` which sets both `HOME` and `USERPROFILE`:
```go
func MockHome(t *testing.T, dir string) {
    t.Setenv("HOME", dir)
    t.Setenv("USERPROFILE", dir)
    t.Setenv("HOMEDRIVE", filepath.VolumeName(dir))
    t.Setenv("HOMEPATH", dir[len(filepath.VolumeName(dir)):])
}
```

### 4.3 Phased Execution Schedule

1. **Phase 1 (Bug Fixes & Platform Stability)**:
   - Patch `resolvePath` traversal vulnerability.
   - Fix Windows test runner issues (`HOME`/`USERPROFILE`, symlink skipping, NTFS permission check).
2. **Phase 2 (CLI Namespace Consolidation with Legacy Aliases)**:
   - Introduce `store`, `pd`, `artifact`, `security` parent commands.
   - Register legacy aliases with deprecation hints.
3. **Phase 3 (MCP Tool Consolidation & Default Dynamic Toolsets)**:
   - Unify `upload_artifact` polymorphic schema.
   - Consolidate store and diagnostic tools.
   - Enable lean default toolset (progressive disclosure).
