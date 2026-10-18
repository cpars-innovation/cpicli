package schemagen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cmd"
	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The results of real commands against the demo landscape match their
// schemas.
func TestResultsMatchSchemas(t *testing.T) {
	l, err := cpitest.BuiltinLandscape("demo")
	require.NoError(t, err)
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{})
	require.NoError(t, cpitest.SeedLandscape(mock, l, l.TierNames()[0], time.Now()))
	host, port := mock.HostPort()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CPICTL_PROFILE", "")
	t.Setenv("CPICTL_CONFIG", "")
	work := t.TempDir()
	lintDir, err := filepath.Abs(filepath.Join("..", "cpitest", "landscapes", "demo", "packages"))
	require.NoError(t, err)
	schemas, err := filepath.Abs(filepath.Join("..", "..", "schemas"))
	require.NoError(t, err)
	t.Chdir(work) // commands write .cpi/ in the working directory

	// the schema file name is the command path: the arguments up to the
	// first flag
	commands := [][]string{
		{"packages"},
		{"artifacts", "--package-id", "Orders"},
		{"status"},
		{"runtimes", "--artifacts"},
		{"logs", "--since", "24h"},
		{"logs", "summary", "--since", "24h"},
		{"pd", "list"},
		{"credentials", "list"},
		{"keystore", "list"},
		{"doctor"},
		{"discover", "--dir", work},
		{"backup", "create", "--dir", work},
		{"lint", "--dir", lintDir},
	}
	ran := 0
	for _, args := range commands {
		var path []string
		for _, a := range args {
			if strings.HasPrefix(a, "-") {
				break
			}
			path = append(path, a)
		}
		schemaFile := filepath.Join(schemas, strings.Join(path, "_")+".json")
		data, err := os.ReadFile(schemaFile)
		if !assert.NoError(t, err, "no schema for %v", args) {
			continue
		}
		var schema map[string]any
		require.NoError(t, json.Unmarshal(data, &schema))

		viper.Reset()
		var stdout, stderr bytes.Buffer
		full := append(append([]string{}, args...), "--output", "json",
			"--tmn-host", "http://"+host+":"+strconv.Itoa(port), "--tmn-userid", "user", "--tmn-password", "secret")
		code := cmd.Run(context.Background(), full, &stdout, &stderr, "test", "")
		var env struct {
			OK     bool `json:"ok"`
			Result any  `json:"result"`
		}
		require.NoError(t, json.Unmarshal(stdout.Bytes(), &env), "%v: %s", args, stderr.String())
		if !assert.True(t, env.OK, "%v exit %d: %s", args, code, tail(stderr.String())) {
			continue
		}
		ran++
		for _, problem := range validate(schema, schema, env.Result, "result") {
			t.Errorf("%v: %s", args, problem)
		}
	}
	viper.Reset()
	assert.Equal(t, len(commands), ran)
}

func tail(s string) string {
	if len(s) > 600 {
		return s[len(s)-600:]
	}
	return s
}

// validate checks v against the subset of JSON schema the generator writes.
func validate(root, s map[string]any, v any, at string) []string {
	if ref, ok := s["$ref"].(string); ok {
		def := root["$defs"].(map[string]any)[strings.TrimPrefix(ref, "#/$defs/")]
		return validate(root, def.(map[string]any), v, at)
	}
	if all, ok := s["allOf"].([]any); ok {
		var out []string
		for _, sub := range all {
			out = append(out, validate(root, sub.(map[string]any), v, at)...)
		}
		return out
	}
	if any_, ok := s["anyOf"].([]any); ok {
		var first []string
		for i, sub := range any_ {
			p := validate(root, sub.(map[string]any), v, at)
			if len(p) == 0 {
				return nil
			}
			if i == 0 {
				first = p
			}
		}
		return append([]string{at + ": matches no anyOf variant"}, first...)
	}
	typ, _ := s["type"].(string)
	switch typ {
	case "":
		return nil
	case "null":
		if v != nil {
			return []string{at + ": want null"}
		}
	case "string":
		if _, ok := v.(string); !ok {
			return []string{fmt.Sprintf("%s: want string, got %T", at, v)}
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return []string{fmt.Sprintf("%s: want boolean, got %T", at, v)}
		}
	case "integer", "number":
		f, ok := v.(float64)
		if !ok || (typ == "integer" && f != float64(int64(f))) {
			return []string{fmt.Sprintf("%s: want %s, got %v", at, typ, v)}
		}
	case "array":
		arr, ok := v.([]any)
		if !ok {
			return []string{fmt.Sprintf("%s: want array, got %T", at, v)}
		}
		var out []string
		if items, ok := s["items"].(map[string]any); ok {
			for i, e := range arr {
				out = append(out, validate(root, items, e, fmt.Sprintf("%s[%d]", at, i))...)
			}
		}
		return out
	case "object":
		obj, ok := v.(map[string]any)
		if !ok {
			return []string{fmt.Sprintf("%s: want object, got %T", at, v)}
		}
		var out []string
		props, _ := s["properties"].(map[string]any)
		for _, r := range asStrings(s["required"]) {
			if _, ok := obj[r]; !ok {
				out = append(out, at+": missing required "+r)
			}
		}
		for k, val := range obj {
			if ps, ok := props[k].(map[string]any); ok {
				out = append(out, validate(root, ps, val, at+"."+k)...)
			} else if ap, ok := s["additionalProperties"].(map[string]any); ok {
				out = append(out, validate(root, ap, val, at+"."+k)...)
			} else if props != nil {
				out = append(out, at+": property not in the schema: "+k)
			}
		}
		return out
	}
	return nil
}

func asStrings(v any) []string {
	var out []string
	l, _ := v.([]any)
	for _, x := range l {
		out = append(out, x.(string))
	}
	return out
}
