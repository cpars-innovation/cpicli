package cpitest_test

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/iflow"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func demo(t *testing.T, tier string) *cpitest.Tenant {
	t.Helper()
	m := cpitest.NewTenant(t, nil)
	require.NoError(t, cpitest.SeedDemo(m, tier, time.Now()))
	return m
}

func TestDemoGraph(t *testing.T) {
	m := demo(t, "dev")
	d, err := ops.DiscoverTenant(context.Background(), m.Executer(), "mock", ops.DiscoverOptions{})
	require.NoError(t, err)
	assert.Len(t, d.IFlows, 8)
	g := ops.BuildGraph(d)
	sends := map[string]bool{}
	for _, e := range g.Edges {
		if e.Type == ops.EdgeSendsTo {
			sends[e.From+">"+e.To] = true
		}
	}
	for _, want := range []string{"Orders_In>Orders_Route", "Orders_Route>Billing_In", "Billing_In>Billing_Post"} {
		from, to, _ := strings.Cut(want, ">")
		assert.True(t, sends[ops.NodeIFlow+":"+from+">"+ops.NodeIFlow+":"+to], "%s in %v", want, sends)
	}
}

func TestDemoModelsAreLaidOut(t *testing.T) {
	m := demo(t, "dev")
	for id, a := range m.Artifacts {
		zr, err := zip.NewReader(bytes.NewReader(a.Zip), int64(len(a.Zip)))
		require.NoError(t, err)
		for _, f := range zr.File {
			if !strings.HasSuffix(f.Name, ".iflw") {
				continue
			}
			rc, err := f.Open()
			require.NoError(t, err)
			data, _ := io.ReadAll(rc)
			_ = rc.Close()
			issues, err := iflow.CheckLayout(data)
			require.NoError(t, err, id)
			assert.Empty(t, issues, id)
			assert.Contains(t, string(data), "BPMNShape", id)
		}
	}
}

func TestDemoTiersDiffer(t *testing.T) {
	dev, prod := demo(t, "dev"), demo(t, "prod")
	assert.Equal(t, "Active", dev.Artifacts["Billing_Post"].DesignVersion, "draft on dev")
	assert.Equal(t, "1.0.2", prod.Artifacts["Orders_Route"].DesignVersion)
	assert.Nil(t, prod.Artifacts["Returns_In"])
	assert.NotContains(t, prod.Credentials["UserCredentials"], "Returns_API")
	assert.Less(t, time.Until(prod.Keystore[0].NotAfter), 30*24*time.Hour, "an expiring certificate on prod")
	assert.Error(t, cpitest.SeedDemo(cpitest.NewTenant(t, nil), "qa", time.Now()))
}

func TestDemoMessages(t *testing.T) {
	m := demo(t, "dev")
	exe := m.Executer()
	sum, err := ops.SummarizeMessages(exe, ops.MessageSummaryQuery{Since: time.Now().Add(-2 * time.Hour)})
	require.NoError(t, err)

	day, err := ops.SummarizeMessages(exe, ops.MessageSummaryQuery{Since: time.Now().Add(-25 * time.Hour), ErrorSamples: 200})
	require.NoError(t, err)
	assert.Positive(t, sum.Scanned)
	assert.Less(t, 5*sum.Scanned, day.Scanned, "the time filter applies")
	require.NotEmpty(t, day.Errors)
	var posting *ops.ErrorGroup
	for i := range day.Errors {
		if day.Errors[i].Artifact == "Billing_Post" {
			posting = &day.Errors[i]
		}
	}
	require.NotNil(t, posting, "postings fail now and then: %v", day.Errors)
	assert.Greater(t, posting.Count, 1, "the same error with different order numbers is one group")

	returns, err := ops.QueryMessageLogs(exe, ops.MessageLogQuery{ArtifactID: "Returns_In", Top: 1})
	require.NoError(t, err)
	require.NotEmpty(t, returns.Logs)
	path, err := ops.MessagePathFor(context.Background(), exe, ops.MessagePathQuery{MessageGuid: returns.Logs[0].MessageGuid,
		KeyHeaders: []string{"OrderNo"}, Scope: ops.ScanScope{ArtifactIDs: []string{"Orders_In"}, Since: time.Now().Add(-25 * time.Hour)}, MaxScan: 500})
	require.NoError(t, err)
	assert.Equal(t, 5, path.Messages, "the order's run joined by OrderNo")
	assert.Equal(t, "correlation_id+header", path.Source)
}

func TestLiveDeployAndSend(t *testing.T) {
	m := demo(t, "test")
	exe := m.Executer()
	m.Artifacts["Orders_Route"].DesignVersion = "1.0.9"
	require.NoError(t, cpi.NewIntegration(exe).Deploy("Orders_Route"))
	version, status, err := cpi.NewRuntime(exe).Get("Orders_Route")
	require.NoError(t, err)
	assert.Equal(t, "1.0.9", version)
	assert.Equal(t, "STARTED", status)

	require.NoError(t, cpi.NewRuntime(exe).UnDeploy("Partner_Notify"))
	rt, err := cpi.NewRuntime(exe).GetArtifact("Partner_Notify")
	require.NoError(t, err)
	assert.Nil(t, rt)

	newExe := func(endpoint string) (*httpclnt.HTTPExecuter, string, error) {
		u, err := url.Parse(endpoint)
		if err != nil {
			return nil, "", err
		}
		return m.Executer().ForEndpoint(u.Path), u.Path, nil
	}
	sent, err := ops.SendTestMessage(context.Background(), exe, newExe, ops.TestMessage{ArtifactID: "Orders_In", Body: []byte("<Order/>")})
	require.NoError(t, err)
	logs, err := ops.QueryMessageLogs(exe, ops.MessageLogQuery{ArtifactID: "Orders_In", Top: 1})
	require.NoError(t, err)
	assert.Equal(t, sent.MessageGuid, logs.Logs[0].MessageGuid, "the newest message is the one sent")
}
