// Package mcp implements a Model Context Protocol server (stdio transport,
// JSON-RPC 2.0, one message per line) that exposes cpicli operations as tools.
package mcp

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/internal/stats"
	"github.com/rs/zerolog/log"
)

// SupportedProtocolVersions lists the MCP revisions this server speaks,
// newest first.
var SupportedProtocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// Tool is one MCP tool.
type Tool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
	// Handler executes the tool. args are the raw "arguments" object.
	Handler func(ctx context.Context, args json.RawMessage) (any, error) `json:"-"`
	// Deprecated names the tool that replaces this one: the tool is not
	// listed any more, but calls still work (with a notice in the result).
	Deprecated string `json:"-"`
}

// Server is an MCP server.
type Server struct {
	Name         string
	Version      string
	Instructions string
	tools        []Tool
	// dynamic is set in dynamic toolset mode (UseDynamicToolsets).
	dynamic *dynamicState
	// Removed are the tools the mode or filters removed, with the reason. A
	// call to one gets an error result (errorCategory usage) instead of the
	// JSON-RPC error of an unknown tool.
	Removed map[string]string
	// Redactor rewrites every tool result before it is sent (masking, payload
	// redaction, root-relative paths); NewServer sets one with the default
	// masking.
	Redactor *Redactor
	// Root is the server root: resources list and read the loop journals
	// under it (.cpi/loops); empty: no loop resources.
	Root string

	writeMu sync.Mutex
	out     io.Writer

	callsMu sync.Mutex
	calls   map[string]context.CancelFunc
}

// NewServer returns a server exposing tools.
func NewServer(name, version, instructions string, tools []Tool) *Server {
	return &Server{Name: name, Version: version, Instructions: instructions, tools: tools, calls: map[string]context.CancelFunc{},
		Redactor: &Redactor{}}
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// Serve reads requests from r and writes responses to w until r is closed or
// ctx is cancelled. Tool calls run concurrently; each can be cancelled with
// notifications/cancelled.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	s.out = w
	reader := bufio.NewReaderSize(r, 1<<20)
	var wg sync.WaitGroup
	defer wg.Wait()

	lines := make(chan []byte)
	readErr := make(chan error, 1)
	go func() {
		defer close(lines)
		for {
			line, tooLong, err := readLine(reader, MaxRequestBytes)
			if tooLong {
				line = tooLongMarker
			}
			if len(bytes.TrimSpace(line)) > 0 {
				select {
				case lines <- line:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					readErr <- err
				}
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case line, ok := <-lines:
			if !ok {
				select {
				case err := <-readErr:
					return err
				default:
					return nil
				}
			}
			if bytes.Equal(line, tooLongMarker) {
				s.write(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: codeParseError,
					Message: fmt.Sprintf("request larger than %d bytes", MaxRequestBytes)}})
				continue
			}
			s.dispatch(ctx, line, &wg)
		}
	}
}

// MaxRequestBytes caps one JSON-RPC message: a longer line is discarded and
// answered with a parse error instead of growing memory without bound.
var MaxRequestBytes = 16 << 20

var tooLongMarker = []byte("\x00too-long")

// readLine reads one line of at most max bytes; a longer line is consumed up
// to its newline and reported as tooLong.
func readLine(r *bufio.Reader, max int) (line []byte, tooLong bool, err error) {
	for {
		chunk, err := r.ReadSlice('\n')
		if !tooLong {
			if len(line)+len(chunk) > max {
				tooLong, line = true, nil
			} else {
				line = append(line, chunk...)
			}
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return line, tooLong, err
		}
	}
}

func (s *Server) dispatch(ctx context.Context, line []byte, wg *sync.WaitGroup) {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		s.write(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: codeParseError, Message: "parse error: " + err.Error()}})
		return
	}
	isNotification := len(req.ID) == 0
	if req.JSONRPC != "2.0" || req.Method == "" {
		if !isNotification {
			s.reply(req.ID, nil, &rpcError{Code: codeInvalidRequest, Message: "invalid JSON-RPC 2.0 request"})
		}
		return
	}

	switch req.Method {
	case "initialize":
		s.reply(req.ID, s.initialize(req.Params), nil)
	case "ping":
		s.reply(req.ID, map[string]any{}, nil)
	case "tools/list":
		s.reply(req.ID, map[string]any{"tools": s.listedTools()}, nil)
	case "resources/list":
		s.reply(req.ID, map[string]any{"resources": s.listResources()}, nil)
	case "resources/read":
		result, rpcErr := s.readResource(req.Params)
		s.reply(req.ID, result, rpcErr)
	case "tools/call":
		// registered before the call starts, so that a cancellation that
		// follows right after the request is not missed
		callCtx, cancel := context.WithCancel(ctx)
		s.callsMu.Lock()
		s.calls[string(req.ID)] = cancel
		s.callsMu.Unlock()
		wg.Go(func() {
			defer func() {
				s.callsMu.Lock()
				delete(s.calls, string(req.ID))
				s.callsMu.Unlock()
				cancel()
			}()
			result, rpcErr := s.callTool(callCtx, req.Params)
			s.reply(req.ID, result, rpcErr)
		})
	case "notifications/cancelled":
		var p struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		if json.Unmarshal(req.Params, &p) == nil {
			s.callsMu.Lock()
			if cancel, ok := s.calls[string(p.RequestID)]; ok {
				cancel()
			}
			s.callsMu.Unlock()
		}
	default:
		if !isNotification { // notifications such as notifications/initialized need no answer
			s.reply(req.ID, nil, &rpcError{Code: codeMethodNotFound, Message: "method not found: " + req.Method})
		}
	}
}

func (s *Server) initialize(params json.RawMessage) any {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &p)
	version := SupportedProtocolVersions[0]
	if slices.Contains(SupportedProtocolVersions, p.ProtocolVersion) {
		version = p.ProtocolVersion
	}
	return map[string]any{
		"protocolVersion": version,
		"capabilities": map[string]any{"tools": map[string]any{"listChanged": s.dynamic != nil},
			"resources": map[string]any{"subscribe": false, "listChanged": false}},
		"serverInfo":   map[string]any{"name": s.Name, "version": s.Version},
		"instructions": s.Instructions,
	}
}

// ToolResult is the structured content of every tool call result.
type ToolResult struct {
	OK bool `json:"ok"`
	// ErrorCategory mirrors the CLI exit codes: usage, auth, tenant_http,
	// failed, timeout, partial, error.
	ErrorCategory string `json:"errorCategory,omitempty"`
	ExitCode      int    `json:"exitCode"`
	Error         string `json:"error,omitempty"`
	// DurationMs is the time the tool call took, in milliseconds.
	DurationMs int64 `json:"durationMs"`
	// Notice is set when the tool is deprecated: which tool to use instead.
	Notice string `json:"notice,omitempty"`
	Result any    `json:"result"`
}

// Category returns the error category name of an exit code (the same names
// as the CLI's JSON output).
func Category(code int) string {
	if code == exitcode.OK {
		return ""
	}
	return cmp.Or(output.Category(code), "error")
}

// categorizer is an error with its own errorCategory (such as
// loop_required), finer than the category of its exit code.
type categorizer interface{ ErrorCategory() string }

// errorCategory returns the errorCategory of a tool error.
func errorCategory(err error) string {
	var c categorizer
	if errors.As(err, &c) {
		return c.ErrorCategory()
	}
	return Category(output.ExitCode(err))
}

// callTool runs a tool; ctx is cancelled by notifications/cancelled.
func (s *Server) callTool(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "invalid tools/call params: " + err.Error()}
	}
	idx := slices.IndexFunc(s.tools, func(t Tool) bool { return t.Name == p.Name })
	if reason, ok := s.Removed[p.Name]; ok && idx < 0 {
		// a tool of cpictl that this server does not offer: a normal error
		// result, so that the agent reads why
		err := output.Usagef("tool %s is not available on this server: %s", p.Name, reason)
		return s.result(ToolResult{ExitCode: exitcode.Usage, ErrorCategory: Category(exitcode.Usage), Error: err.Error()})
	}
	if idx < 0 {
		return nil, &rpcError{Code: codeInvalidParams, Message: "unknown tool: " + p.Name}
	}
	if len(p.Arguments) == 0 || string(p.Arguments) == "null" {
		p.Arguments = json.RawMessage("{}")
	}

	callCtx := ctx
	// with _meta.progressToken long tools report notifications/progress
	if token := progressToken(params); token != nil {
		var stop func()
		callCtx, stop = s.withProgress(callCtx, token)
		defer stop()
	}

	log.Info().Str("tool", p.Name).Msg("Tool call started")
	begin := time.Now()
	value, err := safeCall(callCtx, s.tools[idx], p.Arguments)
	elapsed := time.Since(begin)
	code := output.ExitCode(err)
	stats.Record(p.Name, stats.SourceMCP, code, elapsed)
	res := ToolResult{OK: err == nil, ExitCode: code, DurationMs: elapsed.Milliseconds(), Result: value}
	if r := s.tools[idx].Deprecated; r != "" {
		res.Notice = fmt.Sprintf("%s is deprecated and no longer listed: use %s", p.Name, r)
		log.Warn().Str("tool", p.Name).Msg(res.Notice)
	}
	if s.Redactor != nil {
		res.Result = s.Redactor.Apply(p.Name, value)
	}
	if err != nil {
		res.ErrorCategory = errorCategory(err)
		res.Error = err.Error()
		if s.Redactor != nil {
			res.Error = s.Redactor.relativeText(res.Error)
		}
		log.Warn().Str("tool", p.Name).Int("exitCode", code).Int64("durationMs", res.DurationMs).Msg(err.Error())
	} else {
		log.Info().Str("tool", p.Name).Int64("durationMs", res.DurationMs).Msg("Tool call finished")
	}
	return s.result(res)
}

// result encodes a tool call result: the text content and the structured
// content carry the same JSON.
func (s *Server) result(res ToolResult) (any, *rpcError) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // keep XML payloads readable
	mErr := enc.Encode(res)
	text := bytes.TrimSpace(buf.Bytes())
	if mErr != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "failed to encode result: " + mErr.Error()}
	}
	return map[string]any{
		"content":           []map[string]any{{"type": "text", "text": string(text)}},
		"structuredContent": res,
		"isError":           !res.OK,
	}, nil
}

func safeCall(ctx context.Context, tool Tool, args json.RawMessage) (v any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("internal error in tool %s: %v", tool.Name, r)
		}
	}()
	return tool.Handler(ctx, args)
}

func (s *Server) reply(id json.RawMessage, result any, rpcErr *rpcError) {
	resp := response{JSONRPC: "2.0", ID: id, Error: rpcErr}
	if rpcErr == nil {
		resp.Result = result
	}
	s.write(resp)
}

// notify sends a notification without parameters to the client.
func (s *Server) notify(method string) {
	s.writeMessage(map[string]any{"jsonrpc": "2.0", "method": method})
}

func (s *Server) write(resp response) {
	s.writeMessage(resp)
}

func (s *Server) writeMessage(msg any) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Error().Msgf("failed to encode message: %v", err)
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, _ = s.out.Write(append(data, '\n'))
}
