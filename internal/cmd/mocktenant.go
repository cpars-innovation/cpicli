package cmd

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	_ "github.com/cpars-innovation/cpicli/pkg/mocktenant/sshkeys" // the mock reads uploaded SSH keys
	"math/big"
	"net"
	"os"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func NewMockTenantCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "mock-tenant",
		Short: "Run an in-memory SAP CPI tenant for local development and tests (never a real tenant)",
		Long: `Serve an in-memory mock of the SAP CPI APIs that cpictl uses: packages,
artifacts (download, upload, deploy, undeploy), parameters, runtime status,
endpoints, message processing logs with custom headers, steps and errors,
credentials, keystore and Partner Directory. Basic Auth (with CSRF) and OAuth
client credentials (token at /oauth/token) are accepted.

--credentials any (default) accepts any credentials everywhere. --credentials
strict checks them like a Cloud Foundry tenant with two service keys: the API
user and client (--api-*) manage content through /api/v1, the runtime client
(--runtime-client-*, plan integration-flow) calls the flows' endpoints
(/http/..., /cxf/..., /gw/odata/...). The other key is refused with 403, wrong credentials with
401. The credentials are printed at start; cpictl send takes the runtime client
as CPICTL_RUNTIME_OAUTH_*.

--seed demo loads a demo landscape for --tier dev|test|prod:
  Orders_In -> (ProcessDirect) Orders_Route -> (JMS) Billing_In -> (ProcessDirect)
  Billing_Post, Partner_Notify (timer, SFTP), and on dev Returns_In;
  Orders_IDoc_In (IDoc sender /cxf/orders/idoc, ORDERS05 to JSON, HTTP receiver)
  and Orders_OData_Service (OData V2 sender /gw/odata/Demo/Orders;v=1, entity
  set SalesOrders, IDoc receiver to ERP); a day of messages with the OrderNo
  custom header, IDocs and some failures. The tiers differ
  like real ones: dev is ahead (newer Orders_Route, a new flow, Billing_Post in
  draft), prod has a parameter changed on the tenant, a certificate expiring in
  20 days and no Returns_API credential.
--seed empty starts without content.
--seed-dir loads a landscape directory instead (landscape.yaml and content in
the layout cpictl snapshot writes, see docs/mock-tenant.md): its tiers, systems
and traffic; --tier names one of its tiers.

The tenant behaves live: a deploy starts the designtime version and registers
the flow's endpoints, uploads are recorded, and a message sent to an endpoint
(POST /http/orders/in; IDoc XML to /cxf/<address>; OData requests to
/gw/odata/<namespace>/<service>;v=<n>/<EntitySet>) runs the flows: one message processing log per flow,
ProcessDirect and JMS to the next flows, receivers answered by the landscape's
systems (latency, failures). --live-traffic keeps generating messages.
The /_mock admin API needs "Authorization: Bearer <token>": --admin-token, or a random
token generated at start and printed as CPICTL_MOCK_ADMIN_TOKEN.
State is in memory; a restart resets it.

Plain http is accepted by cpictl for loopback hosts only. To reach the mock
from another container use --tls: a CA and server certificate are generated
for --tls-hosts, the CA is written to --ca-out; point the client at it with
SSL_CERT_FILE.`,
		Example: `  cpictl mock-tenant --tier dev --addr 127.0.0.1:8081
  cpictl mock-tenant --seed-dir ./landscapes/retail-b --tier prod-eu --addr 127.0.0.1:8084
  cpictl mock-tenant --tier dev --credentials strict --runtime-client-id rt-client --runtime-client-secret rt-secret
  cpictl mock-tenant --tier prod --addr 0.0.0.0:8443 --tls --tls-hosts mock-prod,localhost --ca-out /certs/mock-ca.pem`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Annotations:  map[string]string{annotationOffline: "true", annotationNoEnvelope: "true", annotationNoStats: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			addr, _ := cmd.Flags().GetString("addr")
			seed, _ := cmd.Flags().GetString("seed")
			seedDir, _ := cmd.Flags().GetString("seed-dir")
			tier, _ := cmd.Flags().GetString("tier")
			useTLS, _ := cmd.Flags().GetBool("tls")
			if seed != "demo" && seed != "empty" {
				return output.Usagef("--seed %q: demo or empty", seed)
			}
			mode, _ := cmd.Flags().GetString("credentials")
			if mode != "any" && mode != "strict" {
				return output.Usagef("--credentials %q: any or strict", mode)
			}
			auth := &cpitest.Auth{}
			auth.APIUser, _ = cmd.Flags().GetString("api-user")
			auth.APIPassword, _ = cmd.Flags().GetString("api-password")
			auth.APIClient.ID, _ = cmd.Flags().GetString("api-client-id")
			auth.APIClient.Secret, _ = cmd.Flags().GetString("api-client-secret")
			auth.Runtime.ID, _ = cmd.Flags().GetString("runtime-client-id")
			auth.Runtime.Secret, _ = cmd.Flags().GetString("runtime-client-secret")
			if mode == "strict" {
				if auth.APIUser == "" || auth.APIClient.ID == "" || auth.Runtime.ID == "" {
					return output.Usagef("--credentials strict needs --api-user, --api-client-id and --runtime-client-id")
				}
				if auth.Runtime.ID == auth.APIClient.ID || auth.Runtime.ID == auth.APIUser {
					return output.Usagef("--runtime-client-id must differ from --api-client-id and --api-user")
				}
			}
			var landscape *cpitest.Landscape
			if seedDir != "" {
				l, err := cpitest.LoadLandscapeDir(seedDir)
				if err != nil {
					return output.Usage(err)
				}
				landscape, seed = l, l.Name
			}
			var tlsConfig *tls.Config
			scheme := "http"
			if useTLS {
				hosts, _ := cmd.Flags().GetStringSlice("tls-hosts")
				caOut, _ := cmd.Flags().GetString("ca-out")
				if caOut == "" {
					return output.Usagef("--tls needs --ca-out (where the generated CA certificate is written)")
				}
				cfg, caPEM, err := mockTLS(hosts)
				if err != nil {
					return err
				}
				if err := os.WriteFile(caOut, caPEM, 0o644); err != nil {
					return err
				}
				tlsConfig, scheme = cfg, "https"
			}
			l, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}
			m := cpitest.Serve(l, tlsConfig)
			defer m.Close()
			m.Live, m.OAuth, m.FilterMessageLogs = true, true, true
			if mode == "strict" {
				m.Auth = auth
			}
			m.AdminToken, _ = cmd.Flags().GetString("admin-token")
			m.LogRetention, _ = cmd.Flags().GetDuration("log-retention")
			if m.LogRetention < 0 {
				return output.Usagef("--log-retention must not be negative")
			}
			if m.LogRetention == 0 {
				m.LogRetention = -1 // 0 keeps logs forever (up to the cap)
			}
			generatedToken := m.AdminToken == ""
			if generatedToken {
				b := make([]byte, 24)
				if _, err := rand.Read(b); err != nil {
					return err
				}
				m.AdminToken = hex.EncodeToString(b)
			}
			host := l.Addr().String()
			if h, p, err := net.SplitHostPort(host); err == nil && (h == "::" || h == "0.0.0.0") {
				host = net.JoinHostPort("localhost", p)
			}
			m.EndpointBase, _ = cmd.Flags().GetString("public-url")
			if m.EndpointBase == "" {
				m.EndpointBase = scheme + "://" + host
			}
			switch {
			case landscape != nil:
				if err := cpitest.SeedLandscape(m, landscape, tier, time.Now()); err != nil {
					return output.Usage(err)
				}
			case seed == "demo":
				if err := cpitest.SeedDemo(m, tier, time.Now()); err != nil {
					return output.Usage(err)
				}
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Mock CPI tenant (%s, seed %s) on %s://%s\n\n", tier, seed, scheme, host)
			fmt.Fprintf(out, "CPICTL_TMN_HOST=%s://%s\nCPICTL_TMN_USERID=%s\nCPICTL_TMN_PASSWORD=%s\n", scheme, host, auth.APIUser, auth.APIPassword)
			fmt.Fprintf(out, "# or OAuth: CPICTL_OAUTH_HOST=%s CPICTL_OAUTH_CLIENTID=%s CPICTL_OAUTH_CLIENTSECRET=%s\n", host, auth.APIClient.ID, auth.APIClient.Secret)
			fmt.Fprintf(out, "CPICTL_RUNTIME_OAUTH_HOST=%s\nCPICTL_RUNTIME_OAUTH_CLIENTID=%s\nCPICTL_RUNTIME_OAUTH_CLIENTSECRET=%s\n", host, auth.Runtime.ID, auth.Runtime.Secret)
			if mode == "strict" {
				fmt.Fprintln(out, "# credentials strict: the API user or client on /api/v1, the runtime client on /http, /cxf and /gw/odata")
			} else {
				fmt.Fprintln(out, "# credentials any: every credential is accepted (--credentials strict checks them like a tenant)")
			}
			if useTLS {
				caOut, _ := cmd.Flags().GetString("ca-out")
				fmt.Fprintf(out, "SSL_CERT_FILE=%s\n", caOut)
			}
			if generatedToken {
				fmt.Fprintf(out, "CPICTL_MOCK_ADMIN_TOKEN=%s\n", m.AdminToken)
			}
			if speed, _ := cmd.Flags().GetFloat64("live-traffic"); speed > 0 {
				m.StartTraffic(cmd.Context(), speed)
			}
			log.Info().Str("tier", tier).Str("seed", seed).Msgf("Mock tenant listening on %s; stop with Ctrl+C", l.Addr())
			<-cmd.Context().Done()
			return nil
		},
	}
	c.Flags().String("addr", "127.0.0.1:8081", "Listen address")
	c.Flags().String("seed", "demo", "Content: demo or empty")
	c.Flags().String("seed-dir", "", "Landscape directory to load instead of --seed (landscape.yaml + packages/)")
	c.Flags().String("tier", "dev", "Tier of the landscape (demo: "+strings.Join(cpitest.DemoTiers, ", ")+")")
	c.Flags().Float64("live-traffic", 0, "Keep generating the landscape's traffic in real time at this speed (1: as in landscape.yaml, 60: an hour per minute; 0: off)")
	c.Flags().String("public-url", "", "Base URL of the flows' runtime endpoints as clients reach the mock, e.g. https://mock-dev:8443 (default: the listen address)")
	c.Flags().Duration("log-retention", 168*time.Hour, "How long message logs are kept (older ones are dropped; at most 100000 are kept; 0: no age limit)")
	c.Flags().String("admin-token", "", "Bearer token for the /_mock admin API (default: a random token, printed at start)")
	c.Flags().String("credentials", "any", "Credential check: any (accept all) or strict (API key on /api/v1, runtime key on runtime endpoints, like a tenant)")
	c.Flags().String("api-user", "mock", "Basic Auth user of the API")
	c.Flags().String("api-password", "mock", "Password of --api-user")
	c.Flags().String("api-client-id", "mock", "OAuth client ID of the API key (plan api)")
	c.Flags().String("api-client-secret", "mock", "OAuth client secret of the API key")
	c.Flags().String("runtime-client-id", "mock-rt", "OAuth client ID of the runtime key (plan integration-flow), for runtime endpoints")
	c.Flags().String("runtime-client-secret", "mock-rt", "OAuth client secret of the runtime key")
	c.Flags().Bool("tls", false, "Serve HTTPS with a generated certificate (for access from other containers)")
	c.Flags().StringSlice("tls-hosts", []string{"localhost", "127.0.0.1"}, "Host names and IPs of the generated certificate")
	c.Flags().String("ca-out", "", "With --tls: file the generated CA certificate is written to (PEM)")
	return c
}

// mockTLS generates a CA and a server certificate for hosts.
func mockTLS(hosts []string) (*tls.Config, []byte, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "cpictl mock-tenant CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(1, 0, 0), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "cpictl mock-tenant"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(1, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	for _, h := range hosts {
		if ip := net.ParseIP(strings.TrimSpace(h)); ip != nil {
			leaf.IPAddresses = append(leaf.IPAddresses, ip)
		} else if h = strings.TrimSpace(h); h != "" {
			leaf.DNSNames = append(leaf.DNSNames, h)
		}
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, nil, err
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	cert := tls.Certificate{Certificate: [][]byte{leafDER, caDER}, PrivateKey: key}
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), nil
}
