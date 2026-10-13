package testrun

import (
	"errors"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sentWith(status int, response string, log *ops.MessageLogDetail) *observation {
	return &observation{sent: &ops.SentMessage{HTTPStatus: status, Response: ops.Content{Text: response}, Log: log}, elapsed: 100 * time.Millisecond}
}

func logOf(status, errText string, headers map[string]string) *ops.MessageLogDetail {
	start := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Second)
	l := &ops.MessageLogDetail{MessageLog: ops.MessageLog{MessageGuid: "G1", Status: status, ErrorText: errText, LogStart: &start, LogEnd: &end}}
	for k, v := range headers {
		l.CustomHeaderProperties = append(l.CustomHeaderProperties, cpi.NameValue{Name: k, Value: v})
	}
	return l
}

func expectations(fs []Failure) []string {
	out := []string{}
	for _, f := range fs {
		out = append(out, f.Expectation)
	}
	return out
}

// Each expectation type holds for a matching outcome and fails otherwise.
func TestExpectations(t *testing.T) {
	xmlResp := `<?xml version="1.0"?><ns0:Order xmlns:ns0="urn:x"><OrderNo>4711</OrderNo><Item pos="1"><Qty>2</Qty></Item><Item pos="2"><Qty>5</Qty></Item><Status>OK</Status></ns0:Order>`
	jsonResp := `{"order":{"no":"4711","total":9.50,"items":[{"qty":2},{"qty":5}],"paid":true,"note":null,"tags":["a","b"]}}`
	okLog := logOf("COMPLETED", "", map[string]string{"OrderNo": "4711"})
	failedLog := logOf("FAILED", "com.sap.Mapping: field Customer missing", nil)
	steps := &ops.MessageSteps{Runs: []ops.MessageRunWithSteps{{Steps: []cpi.MessageRunStep{{ModelStepID: "CallActivity_1", Activity: "Map Order"}, {ModelStepID: "EndEvent_1", Activity: "End"}}}}}

	for name, tc := range map[string]struct {
		e    Expect
		o    *observation
		want []string // failed expectations
	}{
		"2xx by default":                {Expect{}, sentWith(202, "", nil), nil},
		"non-2xx fails by default":      {Expect{}, sentWith(500, "boom", nil), []string{"http_status"}},
		"http_status":                   {Expect{HTTPStatus: 201}, sentWith(201, "", nil), nil},
		"http_status wrong":             {Expect{HTTPStatus: 201}, sentWith(200, "", nil), []string{"http_status"}},
		"negative case needs no status": {Expect{ErrorContains: StringList{"Customer missing"}}, sentWith(500, "", failedLog), nil},
		"message_status":                {Expect{MessageStatus: "COMPLETED"}, sentWith(200, "", okLog), nil},
		"message_status any case":       {Expect{MessageStatus: "completed"}, sentWith(200, "", okLog), nil},
		"message_status wrong":          {Expect{MessageStatus: "COMPLETED"}, sentWith(200, "", failedLog), []string{"message_status"}},
		"message_status no log":         {Expect{MessageStatus: "COMPLETED"}, &observation{sent: &ops.SentMessage{HTTPStatus: 200}, sendErr: errors.New("message G1 did not reach a final status within 1m0s")}, []string{"message_status"}},
		"response_contains":             {Expect{ResponseContains: StringList{"<Status>OK</Status>", "4711"}}, sentWith(200, xmlResp, nil), nil},
		"response_contains missing":     {Expect{ResponseContains: StringList{"<Status>NOK"}}, sentWith(200, xmlResp, nil), []string{"response_contains"}},
		"response_not_contains":         {Expect{ResponseNotContains: StringList{"<Error>"}}, sentWith(200, xmlResp, nil), nil},
		"response_not_contains present": {Expect{ResponseNotContains: StringList{"<Status>OK"}}, sentWith(200, xmlResp, nil), []string{"response_not_contains"}},
		"response_xpath": {Expect{ResponseXPath: map[string]string{"/Order/OrderNo": "4711", "//Qty": "2", "/Order/Item[@pos='2']/Qty": "5",
			"/Order/Item[2]/@pos": "2", "//Status/text()": "OK", "/ns0:Order/Status": "OK"}}, sentWith(200, xmlResp, nil), nil},
		"response_xpath wrong":    {Expect{ResponseXPath: map[string]string{"/Order/OrderNo": "4712"}}, sentWith(200, xmlResp, nil), []string{"response_xpath[/Order/OrderNo]"}},
		"response_xpath missing":  {Expect{ResponseXPath: map[string]string{"//Nope": "x", "/Order/@nope": "y"}}, sentWith(200, xmlResp, nil), []string{"response_xpath[/Order/@nope]", "response_xpath[//Nope]"}},
		"response_xpath not xml":  {Expect{ResponseXPath: map[string]string{"/a": "x"}}, sentWith(200, jsonResp, nil), []string{"response_xpath[/a]"}},
		"response_xpath bad path": {Expect{ResponseXPath: map[string]string{"/a[": "x"}}, sentWith(200, xmlResp, nil), []string{"response_xpath[/a[]"}},
		"response_json": {Expect{ResponseJSON: map[string]any{"order.no": "4711", "order.total": 9.5, "order.items.1.qty": 5, "order.paid": true,
			"order.note": nil, "order.tags": []any{"a", "b"}, "order.items.0": map[string]any{"qty": 2}}}, sentWith(200, jsonResp, nil), nil},
		"response_json number as text": {Expect{ResponseJSON: map[string]any{"order.items.0.qty": "2"}}, sentWith(200, jsonResp, nil), nil},
		"response_json wrong":          {Expect{ResponseJSON: map[string]any{"order.no": "4712", "order.paid": false}}, sentWith(200, jsonResp, nil), []string{"response_json[order.no]", "response_json[order.paid]"}},
		"response_json missing":        {Expect{ResponseJSON: map[string]any{"order.items.7.qty": 1, "order.no.x": 1}}, sentWith(200, jsonResp, nil), []string{"response_json[order.items.7.qty]", "response_json[order.no.x]"}},
		"response_json not json":       {Expect{ResponseJSON: map[string]any{"a": 1}}, sentWith(200, xmlResp, nil), []string{"response_json[a]"}},
		"error_contains":               {Expect{HTTPStatus: 500, ErrorContains: StringList{"field Customer"}}, sentWith(500, "", failedLog), nil},
		"error_contains in response":   {Expect{HTTPStatus: 500, ErrorContains: StringList{"Read timed out"}}, sentWith(500, "java.net.SocketTimeoutException: Read timed out", nil), nil},
		"error_contains no error":      {Expect{ErrorContains: StringList{"field Customer"}}, sentWith(200, "", okLog), []string{"error_contains"}},
		"custom_headers":               {Expect{CustomHeaders: map[string]string{"OrderNo": "4711"}}, sentWith(200, "", okLog), nil},
		"custom_headers wrong":         {Expect{CustomHeaders: map[string]string{"OrderNo": "1", "Partner": "ACME"}}, sentWith(200, "", okLog), []string{"custom_headers[OrderNo]", "custom_headers[Partner]"}},
		"steps_include":                {Expect{StepsInclude: StringList{"Map Order", "EndEvent_1"}}, func() *observation { o := sentWith(200, "", okLog); o.steps = steps; return o }(), nil},
		"steps_include missing":        {Expect{StepsInclude: StringList{"Call ERP"}}, func() *observation { o := sentWith(200, "", okLog); o.steps = steps; return o }(), []string{"steps_include"}},
		"steps_include unreadable":     {Expect{StepsInclude: StringList{"x"}}, func() *observation { o := sentWith(200, "", okLog); o.stepsErr = errors.New("404"); return o }(), []string{"steps_include"}},
		"max_duration of the message":  {Expect{MaxDuration: Duration(3 * time.Second)}, sentWith(200, "", okLog), nil},
		"max_duration exceeded":        {Expect{MaxDuration: Duration(time.Second)}, sentWith(200, "", okLog), []string{"max_duration"}},
		"max_duration of the call":     {Expect{MaxDuration: Duration(50 * time.Millisecond)}, sentWith(200, "", nil), []string{"max_duration"}},
	} {
		t.Run(name, func(t *testing.T) {
			got := expectations(check(tc.e, tc.o))
			if tc.want == nil {
				tc.want = []string{}
			}
			assert.ElementsMatch(t, tc.want, got)
		})
	}
}

func TestFailureMessages(t *testing.T) {
	fs := check(Expect{MessageStatus: "COMPLETED", ResponseXPath: map[string]string{"/a/b": "1"}, MaxDuration: Duration(time.Second)},
		sentWith(200, "<a><b>2</b></a>", logOf("FAILED", "mapping failed", nil)))
	require.Len(t, fs, 3)
	assert.Equal(t, Failure{Expectation: "message_status", Expected: "COMPLETED", Actual: "FAILED", Message: "message status FAILED, expected COMPLETED: mapping failed"}, fs[0])
	assert.Equal(t, Failure{Expectation: "response_xpath[/a/b]", Expected: "1", Actual: "2", Message: `/a/b is "2", expected "1"`}, fs[1])
	assert.Equal(t, Failure{Expectation: "max_duration", Expected: "1s", Actual: "2s", Message: "the message took 2s, more than 1s"}, fs[2])
}

func TestStubMatchers(t *testing.T) {
	calls := []ops.StubCall{
		{Rule: "erp", Method: "POST", Path: "erp/orders/4711", Body: `{"no":"4711"}`, Headers: map[string]string{"X-Order": "4711"}},
		{Rule: "erp", Method: "GET", Path: "erp/orders"},
	}
	assert.Empty(t, checkStubs([]StubMatcher{{Rule: "erp", Count: intp(2)}, {Method: "post", PathContains: "/4711", BodyContains: StringList{`"no"`}, Header: map[string]string{"x-order": "4711"}}, {Rule: "crm", Count: intp(0)}}, calls, nil))
	fs := checkStubs([]StubMatcher{{Rule: "erp"}, {Header: map[string]string{"X-Order": "1"}}}, calls, nil)
	require.Len(t, fs, 2)
	assert.Equal(t, "stub_calls[0]", fs[0].Expectation)
	assert.Equal(t, "2 stub call(s) with rule erp, expected 1", fs[0].Message)
	assert.Equal(t, `2 of 2 recorded call(s)`, fs[0].Actual)
	assert.Contains(t, fs[1].Message, `header X-Order="1"`)
	assert.Equal(t, "(any)", StubMatcher{}.String())
}

func TestXPathAndJSONPath(t *testing.T) {
	v, ok, err := XPath([]byte(`<a x="1"><b>t<c>u</c></b></a>`), "/a/b")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "tu", v, "text of the element and its descendants")
	v, ok, _ = XPath([]byte(`<a x="1"/>`), "/a/@x")
	assert.True(t, ok)
	assert.Equal(t, "1", v)

	got, ok, err := JSONPath([]byte(`[{"a":1}]`), "0.a")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "1", scalar(got))
	got, ok, _ = JSONPath([]byte(`{"a":1}`), ".")
	assert.True(t, ok)
	assert.Equal(t, `{"a":1}`, scalar(got))
}
