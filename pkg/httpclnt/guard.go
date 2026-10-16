package httpclnt

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync/atomic"
)

// Guard checks every request an executer sends and may rewrite the body of
// the answer (to hide entries a scope does not grant). It is installed for the
// whole process with SetGuard, so no executer can be created without it.
type Guard interface {
	// Before refuses a request by returning an error; nothing is sent then.
	Before(r *GuardRequest) error
	// After returns the body the caller sees (body itself when unchanged).
	After(r *GuardRequest, status int, header http.Header, body []byte) ([]byte, error)
}

// GuardRequest is one request as the guard sees it.
type GuardRequest struct {
	Ctx    context.Context
	Method string
	// Base is scheme://host:port of the executer; Path includes the query.
	Base, Path string
	Header     map[string]string
	Body       []byte
}

var processGuard atomic.Pointer[Guard]

// SetGuard installs g for every executer of the process (nil removes it).
func SetGuard(g Guard) {
	if g == nil {
		processGuard.Store(nil)
		return
	}
	processGuard.Store(&g)
}

// CurrentGuard is the installed guard, or nil.
func CurrentGuard() Guard {
	if p := processGuard.Load(); p != nil {
		return *p
	}
	return nil
}

// guarded sends a request through the process guard, if any.
func (e *HTTPExecuter) guarded(method, base, path string, payload []byte, headers map[string]string, do func() (*http.Response, error)) (*http.Response, error) {
	g := CurrentGuard()
	if g == nil {
		return do()
	}
	r := &GuardRequest{Ctx: e.Context(), Method: method, Base: base, Path: path, Header: headers, Body: payload}
	if err := g.Before(r); err != nil {
		return nil, err
	}
	resp, err := do()
	if err != nil {
		return resp, err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	out, err := g.After(r, resp.StatusCode, resp.Header, body)
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(out))
	resp.ContentLength = int64(len(out))
	return resp, nil
}
