package ops

import (
	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
)

// Exit codes of cpictl, also returned by ExitCode. Every error a pkg/ops
// function returns maps to one of them.
const (
	ExitOK           = exitcode.OK
	ExitError        = exitcode.Error        // unexpected error
	ExitUsage        = exitcode.Usage        // invalid input, unknown object, refused option
	ExitAuth         = exitcode.Auth         // 401/403, OAuth token
	ExitTenantHTTP   = exitcode.TenantHTTP   // other HTTP error or tenant unreachable
	ExitDeployFailed = exitcode.DeployFailed // deployment, validation or check failed on the tenant
	ExitTimeout      = exitcode.Timeout      // the tenant did not finish in time
	ExitPartial      = exitcode.Partial      // some items failed
	ExitStopped      = exitcode.Stopped      // an MCP loop limit stopped tenant changes
)

// Error categories returned by ErrorCategory; the same names as the
// errorCategory of MCP tool results and of the CLI's JSON envelope.
const (
	CategoryUsage      = "usage"
	CategoryAuth       = "auth"
	CategoryTenantHTTP = "tenant_http"
	CategoryFailed     = "failed"
	CategoryTimeout    = "timeout"
	CategoryPartial    = "partial"
	CategoryStopped    = "stopped"
	CategoryError      = "error"
)

// ExitCode classifies an error as cpictl does (0 for nil). It looks at the
// error chain: errors with an ExitCode method (usage, partial, failed,
// timeout, deploy errors), authentication and tenant HTTP errors.
func ExitCode(err error) int { return output.ExitCode(err) }

// ErrorCategory names the class of an error ("" for nil): usage, auth,
// tenant_http, failed, timeout, partial, stopped or error.
func ErrorCategory(err error) string { return output.Category(output.ExitCode(err)) }
