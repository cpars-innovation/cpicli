package cmd

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func newRuntimesWatchCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "watch",
		Short: "Report deployments, status changes and undeployments on the tenant's runtime as events",
		Long: `Reads the runtime of the tenant with one request per interval, however many
flows are deployed, and reports every change as an event: deployed (new on the
runtime or deployed again), started, error (with the tenant's error text),
status (any other status change) and undeployed. Deployments from anywhere count:
cpictl, the Web UI, other tools.

Each event is one JSON line on standard output; logs go to standard error. With
--webhook every event is also POSTed as JSON to that URL (3 attempts). With
--webhook-secret (or CPICTL_WEBHOOK_SECRET) the request carries
X-Cpictl-Signature: sha256=<HMAC-SHA256 of the body>, so the receiver can check
that it comes from this watcher. --cells also reads the runtimes the public API
does not list (integration cells) through the Web UI's runtime list.

It runs until stopped (Ctrl-C) or --duration ends. A failed read is logged and
retried at the next interval; an authentication error ends it.`,
		Example: `  cpictl runtimes watch
  cpictl runtimes watch --interval 5s --artifacts 'SD_*' --initial
  cpictl runtimes watch --webhook https://studio.example.com/hooks/cpi --webhook-header "Authorization: Bearer $TOKEN"`,
		SilenceUsage: true,
		Annotations:  map[string]string{annotationNoEnvelope: "true", annotationNoStats: "true"},
		RunE:         runRuntimesWatch,
	}
	c.Flags().Duration("interval", 10*time.Second, "Time between two reads of the runtime")
	c.Flags().Duration("duration", 0, "Stop after this time (default: until stopped)")
	c.Flags().StringSlice("artifacts", nil, "Only these artifact IDs (patterns with *)")
	c.Flags().Bool("cells", false, "Also watch integration cells (Web UI runtime list: more requests per interval)")
	c.Flags().Bool("initial", false, "First report every deployed artifact (event type present)")
	c.Flags().String("webhook", "", "POST every event as JSON to this URL (https; http only for localhost)")
	c.Flags().StringArray("webhook-header", nil, "Extra request header for the webhook, \"Name: value\" (repeatable)")
	c.Flags().String("webhook-secret", "", "Sign webhook requests with HMAC-SHA256 (default: CPICTL_WEBHOOK_SECRET)")
	return c
}

func runRuntimesWatch(cmd *cobra.Command, args []string) error {
	interval, _ := cmd.Flags().GetDuration("interval")
	duration, _ := cmd.Flags().GetDuration("duration")
	patterns, _ := cmd.Flags().GetStringSlice("artifacts")
	cells, _ := cmd.Flags().GetBool("cells")
	initial, _ := cmd.Flags().GetBool("initial")
	hook, err := newWebhook(cmd)
	if err != nil {
		return err
	}
	for _, p := range patterns {
		if _, err := path.Match(p, ""); err != nil {
			return output.Usagef("--artifacts %q: %v", p, err)
		}
	}
	ctx := cmd.Context()
	if duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, duration)
		defer cancel()
	}
	opts := ops.WatchOptions{Interval: interval, Cells: cells, Initial: initial,
		OnError: func(err error) { log.Warn().Msgf("Reading the runtime failed (next try in %s): %v", interval, err) }}
	if len(patterns) > 0 {
		opts.Match = func(id string) bool {
			for _, p := range patterns {
				if ok, _ := path.Match(p, id); ok {
					return true
				}
			}
			return false
		}
	}
	out := json.NewEncoder(cmd.OutOrStdout())
	log.Info().Msgf("Watching the runtime every %s%s", interval, map[bool]string{true: " (with integration cells)", false: ""}[cells])
	err = ops.WatchRuntime(ctx, tenantExecuter(cmd), opts, func(ev ops.RuntimeEvent) {
		_ = out.Encode(ev)
		where := ""
		if ev.Runtime != "" {
			where = " on " + ev.Runtime
		}
		log.Info().Msgf("%s %s %s%s %s", ev.Type, ev.ID, ev.Version, where, ev.Status)
		if hook != nil {
			hook.send(ctx, ev)
		}
	})
	if err != nil && (ctx.Err() == context.DeadlineExceeded || cmd.Context().Err() != nil) {
		return nil // --duration ended or stopped
	}
	return err
}

// webhook posts events to a URL.
type webhook struct {
	url     string
	headers http.Header
	secret  []byte
	client  *http.Client
}

func newWebhook(cmd *cobra.Command) (*webhook, error) {
	raw, _ := cmd.Flags().GetString("webhook")
	if raw == "" {
		return nil, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, output.Usagef("--webhook %q is not a URL", raw)
	}
	host := u.Hostname()
	local := host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return nil, output.Usagef("--webhook must be https (http only for localhost)")
	}
	h := &webhook{url: raw, headers: http.Header{}, client: &http.Client{Timeout: 10 * time.Second}}
	values, _ := cmd.Flags().GetStringArray("webhook-header")
	for _, v := range values {
		name, value, ok := strings.Cut(v, ":")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, output.Usagef("--webhook-header %q: expected \"Name: value\"", v)
		}
		h.headers.Add(strings.TrimSpace(name), strings.TrimSpace(value))
	}
	secret, _ := cmd.Flags().GetString("webhook-secret")
	if secret == "" {
		secret = os.Getenv("CPICTL_WEBHOOK_SECRET")
	}
	h.secret = []byte(secret)
	return h, nil
}

// send posts one event, up to 3 attempts; a failure is logged, the watch
// goes on.
func (h *webhook) send(ctx context.Context, ev ops.RuntimeEvent) {
	body, _ := json.Marshal(ev)
	var last error
	for attempt := 1; attempt <= 3; attempt++ {
		if attempt > 1 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(attempt-1) * time.Second):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url, bytes.NewReader(body))
		if err != nil {
			last = err
			break
		}
		req.Header = h.headers.Clone()
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Cpictl-Event", ev.Type)
		if len(h.secret) > 0 {
			req.Header.Set("X-Cpictl-Signature", "sha256="+webhookSignature(h.secret, body))
		}
		resp, err := h.client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 300 {
				return
			}
			err = fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		last = err
	}
	log.Warn().Msgf("Webhook for %s %s failed: %v", ev.Type, ev.ID, last)
}

// webhookSignature is the hex HMAC-SHA256 of body.
func webhookSignature(secret, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}
