package mcp

import "github.com/cpars-innovation/cpicli/pkg/ops"

// Tracing exposes payloads, so set_log_level is time-boxed: the server sets
// the flow back to INFO on the first tool call after the deadline and when it
// shuts down (ops.LogLevelReverter).

// LogLevelReverter remembers pending log level reverts.
type LogLevelReverter = ops.LogLevelReverter

// NewLogLevelReverter returns a reverter that uses exe.
var NewLogLevelReverter = ops.NewLogLevelReverter
