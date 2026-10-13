package cmd

import (
	"io"
	"os"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// addRuntimeAuthFlags adds the credentials used for runtime endpoints
// (sending test messages). They usually belong to a separate service key
// (Process Integration Runtime, plan integration-flow) with the role
// ESBMessaging.send.
func addRuntimeAuthFlags(c *cobra.Command) {
	c.Flags().String("runtime-oauth-host", "", "OAuth token server host for runtime endpoints (default: --oauth-host)")
	c.Flags().String("runtime-oauth-clientid", "", "OAuth client ID for runtime endpoints (default: the API credentials)")
	c.Flags().String("runtime-oauth-clientsecret", "", "OAuth client secret for runtime endpoints")
	c.Flags().String("runtime-userid", "", "User ID for Basic Auth on runtime endpoints")
	c.Flags().String("runtime-password", "", "Password for Basic Auth on runtime endpoints")
}

// runtimeDetails returns the runtime endpoint credentials, falling back to
// the API credentials when none are set.
func runtimeDetails(cmd *cobra.Command) *cpi.ServiceDetails {
	switch {
	case config.GetString(cmd, "runtime-oauth-clientid") != "":
		host := config.GetString(cmd, "runtime-oauth-host")
		if host == "" {
			host = config.GetString(cmd, "oauth-host")
		}
		return &cpi.ServiceDetails{OauthHost: host, OauthPath: config.GetString(cmd, "oauth-path"),
			OauthClientId: config.GetString(cmd, "runtime-oauth-clientid"), OauthClientSecret: config.GetString(cmd, "runtime-oauth-clientsecret")}
	case config.GetString(cmd, "runtime-userid") != "":
		return &cpi.ServiceDetails{Userid: config.GetString(cmd, "runtime-userid"), Password: config.GetString(cmd, "runtime-password")}
	}
	return serviceDetails(cmd)
}

func endpointExecuter(cmd *cobra.Command) ops.EndpointExecuterFunc {
	details := runtimeDetails(cmd)
	return func(url string) (*httpclnt.HTTPExecuter, string, error) {
		return cpi.NewEndpointExecuter(details, url)
	}
}

func NewSendCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "send",
		Short: "Send a test message to a deployed integration flow",
		Long: `Send a test message to an endpoint of a deployed integration flow and print
the HTTP status, the response and the message GUID. Only endpoint URLs that the
tenant lists for the flow (see 'endpoints') are used.

With --wait the command also waits for the message processing log and exits
with code 5 when the message did not complete. The message is processed like any
other, including calls to receivers: use test data on a development tenant.

Runtime endpoints usually need other credentials than the API: set
--runtime-oauth-clientid/--runtime-oauth-clientsecret (CPICTL_RUNTIME_OAUTH_*)
from a service key of plan integration-flow with role ESBMessaging.send.
Without them the API credentials are used.

Flows started by ProcessDirect have no endpoint of their own: --process-direct
sends the message to the test harness flow (--harness), which forwards it to the
given address; --artifact-id is then the flow behind the address and --wait
reports that flow's message (found by correlation ID). See docs/testing.md.

SAP senders: --idoc sends IDoc XML to the flow's IDoc sender (POST, Content-Type
application/x-sap.idoc); it must hold an EDI_DC40 control record, and the result
lists DOCNUM and IDOCTYP of each IDoc ('cpictl idoc template' writes a
skeleton). --odata-method, --entity-set, --key and --query build an OData V2
request against the flow's OData sender service (Accept: application/json; the
body of POST, PUT and PATCH is sent as application/json unless --content-type
says otherwise).`,
		Example: `  # Send a file and wait for the outcome
  cpictl send --artifact-id OrderIntake --body-file order.xml --content-type application/xml --wait 60s

  # Body from stdin, extra header
  echo '{"id":1}' | cpictl send --artifact-id OrderIntake --body-file - --header X-Test=1

  # A flow with a ProcessDirect sender, through the test harness
  cpictl send --artifact-id Billing --process-direct /billing/in --body-file invoice.xml --wait 60s

  # An IDoc to a flow with an IDoc sender
  cpictl send --artifact-id Orders_IDoc_In --idoc orders.xml --wait 60s

  # OData: read an entity, create one
  cpictl send --artifact-id Orders_OData_Service --entity-set SalesOrders --key "'4711'"
  cpictl send --artifact-id Orders_OData_Service --odata-method POST --entity-set SalesOrders --body '{"OrderNo":"4711"}'`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ops.GuardProduction(config.GetString(cmd, "tier"), config.GetBool(cmd, "allow-production"), "sending a test message"); err != nil {
				return err
			}
			body, err := readBody(cmd)
			if err != nil {
				return err
			}
			headers := map[string]string{}
			for _, h := range config.GetStringSlice(cmd, "header") {
				k, v, ok := strings.Cut(h, "=")
				if !ok || k == "" {
					return output.Usagef("invalid --header %q, expected name=value", h)
				}
				headers[k] = v
			}
			wait, _ := cmd.Flags().GetDuration("wait")
			msg := ops.TestMessage{
				ArtifactID: config.GetString(cmd, "artifact-id"), URL: config.GetString(cmd, "url"), Path: config.GetString(cmd, "path"),
				Method: config.GetString(cmd, "method"), Body: body, ContentType: config.GetString(cmd, "content-type"),
				Headers: headers, Wait: wait, PollInterval: 5 * time.Second,
				ProcessDirectAddress: config.GetString(cmd, "process-direct"), Harness: config.GetString(cmd, "harness"),
				NoTrace: config.GetBool(cmd, "no-trace"),
			}
			if err := sapMessage(cmd, &msg); err != nil {
				return err
			}
			sent, err := ops.SendTestMessage(cmd.Context(), tenantExecuter(cmd), endpointExecuter(cmd), msg)
			if sent != nil {
				output.SetResult(cmd.Context(), sent)
				for _, c := range sent.IDocs {
					log.Info().Msgf("IDoc %s %s (%s) from %s to %s", c.DOCNUM, c.IDOCTYP, c.MESTYP, c.SNDPRN, c.RCVPRN)
				}
				log.Info().Msgf("%s answered HTTP %d, message %s", sent.URL, sent.HTTPStatus, sent.MessageGuid)
				if sent.Log != nil {
					log.Info().Msgf("Message status %s %s", sent.Log.Status, sent.Log.ErrorText)
				}
			}
			return err
		},
	}
	c.Flags().String("artifact-id", "", "Integration flow ID (deployed)")
	c.Flags().String("url", "", "Endpoint URL (or a URL below it), only needed when the flow has several endpoints")
	c.Flags().String("path", "", "Sub-path and query appended to the flow's endpoint, for senders that route on the path (e.g. test: .../cxf/out/test)")
	c.Flags().String("method", "", "HTTP method (default POST)")
	c.Flags().String("body", "", "Message body")
	c.Flags().String("body-file", "", "Read the message body from this file ('-' for stdin)")
	c.Flags().String("content-type", "", "Content-Type of the body")
	c.Flags().StringSlice("header", nil, "Additional header name=value (repeatable)")
	c.Flags().Duration("wait", 0, "Wait up to this long for the message processing log (e.g. 60s)")
	c.Flags().String("process-direct", "", "Send through the test harness flow to this ProcessDirect address (flows without an HTTP sender)")
	c.Flags().String("harness", ops.DefaultHarnessID, "Test harness flow ID (with --process-direct)")
	c.Flags().Bool("no-trace", false, "Do not send a W3C traceparent header (by default one is generated unless --header traceparent=... is given; its traceId is in the result)")
	c.Flags().String("idoc", "", "Send this IDoc XML file ('-' for stdin) to the flow's IDoc sender")
	c.Flags().String("odata-method", "", "OData request to the flow's OData sender: GET (default with --entity-set), POST, PUT, PATCH or DELETE")
	c.Flags().String("entity-set", "", "OData entity set (or $metadata); without it the service document")
	c.Flags().String("key", "", "OData entity key as written in the parentheses, e.g. \"'4711'\" or OrderNo='4711',Item=10")
	c.Flags().String("query", "", "OData query, e.g. \"$filter=Status eq 'OPEN'&$top=10\"")
	addRuntimeAuthFlags(c)
	_ = c.MarkFlagRequired("artifact-id")
	return c
}

// sapMessage applies --idoc and the OData flags to msg.
func sapMessage(cmd *cobra.Command, msg *ops.TestMessage) error {
	if file := config.GetString(cmd, "idoc"); file != "" {
		var err error
		if file == "-" {
			msg.IDoc, err = io.ReadAll(cmd.InOrStdin())
		} else {
			msg.IDoc, err = os.ReadFile(file)
		}
		if err != nil {
			return output.Usage(err)
		}
	}
	method, set, key, query := config.GetString(cmd, "odata-method"), config.GetString(cmd, "entity-set"), config.GetString(cmd, "key"), config.GetString(cmd, "query")
	if method != "" || set != "" || key != "" || query != "" {
		if config.GetString(cmd, "method") != "" {
			return output.Usagef("use --odata-method for OData requests, not --method")
		}
		msg.OData = &ops.ODataRequest{Method: method, EntitySet: set, Key: key, Query: query}
	}
	return nil
}

func readBody(cmd *cobra.Command) ([]byte, error) {
	text, file := config.GetString(cmd, "body"), config.GetString(cmd, "body-file")
	switch {
	case text != "" && file != "":
		return nil, output.Usagef("use either --body or --body-file")
	case file == "-":
		return io.ReadAll(cmd.InOrStdin())
	case file != "":
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, output.Usage(err)
		}
		return b, nil
	}
	return []byte(text), nil
}
