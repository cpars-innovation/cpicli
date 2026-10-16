// Package exitcode defines the process exit codes of cpictl. They are part of
// the agent/CI contract and must not be renumbered.
package exitcode

const (
	OK           = 0 // success
	Error        = 1 // unexpected/internal error
	Usage        = 2 // invalid usage or configuration
	Auth         = 3 // authentication/authorisation failed (401/403, OAuth token)
	TenantHTTP   = 4 // tenant returned an HTTP error or was unreachable
	DeployFailed = 5 // deployment/validation failed on the tenant
	Timeout      = 6 // operation did not finish within the polling budget
	Partial      = 7 // some items succeeded, some failed
	Stopped      = 8 // an MCP build loop limit was reached; tenant changes are refused
	Scope        = 9 // the operation is outside the access scope (--scope-file)
)
