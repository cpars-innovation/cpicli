package ops

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func restoreCert(t *testing.T, cn string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn}, NotBefore: t0, NotAfter: t0.AddDate(2, 0, 0)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	return der
}

// restoreSource is a tenant with something in every area a restore covers.
func restoreSource(t *testing.T) *cpitest.Tenant {
	t.Helper()
	m := backupTenant(t)
	m.Artifacts["A"].Parameters = map[string]string{"Host": "erp.example.com", "Port": "443"}
	m.Artifacts["A"].Zip = zipOf(t, map[string]string{"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\nBundle-SymbolicName: A\n",
		"src/main/resources/parameters.prop": "Host=erp.example.com\nPort=443\n"})
	m.Packages = append(m.Packages, cpitest.Package{ID: "SAPStd", Name: "SAP standard", Version: "1.0.0", ShortText: "s", Vendor: "SAP", Mode: "READ_ONLY"})
	m.PDStrings = map[string]string{"ACME/Region": "eu10"}
	m.Credentials = map[string]map[string]map[string]any{
		"UserCredentials":  {"ERP_User": {"Name": "ERP_User", "Kind": "default", "User": "erp", "Password": "old"}},
		"SecureParameters": {"Webhook_Key": {"Name": "Webhook_Key", "SecureParam": "k"}},
	}
	m.Keystore = []cpitest.KeystoreEntry{
		{Alias: "partner_ca", DER: restoreCert(t, "Partner CA"), NotAfter: t0.AddDate(2, 0, 0)},
		{Alias: "signing", DER: restoreCert(t, "signing.example.com"), NotAfter: t0.AddDate(2, 0, 0), Type: "KeyPair"},
	}
	m.AccessPolicies = []cpitest.AccessPolicy{{ID: 1, RoleName: "SD_Restricted", Description: "SD only",
		References: []cpitest.ArtifactRefs{{ID: 1, Name: "A", Type: "INTEGRATION_FLOW", ConditionAttribute: "Name", ConditionValue: "A", ConditionType: "EXACT_MATCH"}}}}
	m.NumberRanges = map[string]map[string]string{"Invoice": {"Name": "Invoice", "Description": "invoices", "MinValue": "1", "MaxValue": "999999",
		"CurrentValue": "4711", "FieldLength": "6", "Rotate": "false"}}
	return m
}

func steps(res *RestoreResult, area string) map[string]string {
	out := map[string]string{}
	for _, s := range res.Steps {
		if s.Area == area {
			out[s.Object] = s.Action
		}
	}
	return out
}

// A backup restored into an empty tenant: the dry run plans everything and
// writes nothing; the restore then brings back every area the API takes,
// and lists the rest as manual steps.
func TestRestoreIntoEmptyTenant(t *testing.T) {
	src := restoreSource(t)
	archive := filepath.Join(t.TempDir(), "b.tar.gz")
	res, err := CreateBackup(context.Background(), src.Executer(), "source.it-cpi018.cfapps.eu10.hana.ondemand.com", BackupOptions{Output: archive})
	require.NoError(t, err)
	require.True(t, res.Manifest.Complete, "%v", res.Manifest.Warnings)

	dst := cpitest.NewTenant(t, map[string]*cpitest.Artifact{})
	dst.NumberRanges = map[string]map[string]string{}
	dst.Live = true
	env := map[string]string{"CPI_SECRET_USER_ERP_USER": "restored-secret"}
	opts := RestoreOptions{DryRun: true, Getenv: func(k string) string { return env[k] }, TargetHost: "target.it-cpi018.cfapps.eu10.hana.ondemand.com",
		Deploy: Options{Interval: time.Millisecond, MaxChecks: 50}}

	plan, err := RestoreBackup(context.Background(), dst.Executer(), archive, opts)
	require.NoError(t, err)
	assert.True(t, plan.DryRun)
	assert.Equal(t, map[string]string{"P": RestoreCreate, "SAPStd": RestoreManual}, steps(plan, RestorePackages))
	assert.Equal(t, map[string]string{"A": RestoreCreate, "Lib": RestoreCreate}, steps(plan, RestoreArtifacts))
	assert.Equal(t, map[string]string{"A": RestoreUpdate}, steps(plan, RestoreParameters))
	assert.Equal(t, map[string]string{"ACME/Region": RestoreCreate}, steps(plan, RestorePartnerDirectory))
	assert.Equal(t, map[string]string{"ERP_User": RestoreCreate, "Webhook_Key": RestoreManual}, steps(plan, RestoreCredentials))
	assert.Equal(t, map[string]string{"partner_ca": RestoreCreate, "signing": RestoreManual}, steps(plan, RestoreCertificates))
	assert.Equal(t, map[string]string{"SD_Restricted": RestoreCreate}, steps(plan, RestoreAccessPolicies))
	assert.Equal(t, map[string]string{"Invoice": RestoreCreate}, steps(plan, RestoreNumberRanges))
	assert.Equal(t, map[string]string{"A": RestoreDeployIt}, steps(plan, RestoreDeploy))
	assert.Contains(t, steps(plan, "backup")[archive], RestoreInfo, "another tenant is noted")
	// nothing written
	assert.Empty(t, dst.Packages)
	assert.Empty(t, dst.Artifacts)
	assert.Empty(t, dst.NumberRanges)

	// the plan as a file, without secrets
	for _, name := range []string{"plan.md", "plan.json"} {
		file := filepath.Join(t.TempDir(), name)
		require.NoError(t, WriteRestorePlan(plan, file))
		data, err := os.ReadFile(file)
		require.NoError(t, err)
		assert.NotContains(t, string(data), "restored-secret")
		assert.Contains(t, string(data), "CPI_SECRET_SECURE_WEBHOOK_KEY", "the manual step names the variable")
	}
	md := string(RestorePlanMarkdown(plan))
	assert.Contains(t, md, "# Restore plan (dry run: nothing was changed)")
	assert.Contains(t, md, "## Manual steps")
	assert.Contains(t, md, "- [ ] **certificates** `signing`")

	opts.DryRun = false
	done, err := RestoreBackup(context.Background(), dst.Executer(), archive, opts)
	require.NoError(t, err, "%+v", done.Steps)
	assert.Zero(t, done.Counts[RestoreFailed])
	require.Contains(t, dst.Artifacts, "A")
	require.Contains(t, dst.Artifacts, "Lib")
	assert.Equal(t, "erp.example.com", dst.Artifacts["A"].Parameters["Host"])
	assert.Equal(t, "eu10", dst.PDStrings["ACME/Region"])
	assert.Equal(t, "restored-secret", dst.Credentials["UserCredentials"]["ERP_User"]["Password"])
	assert.NotContains(t, dst.Credentials["SecureParameters"], "Webhook_Key", "no secret given: manual")
	assert.True(t, strings.Contains(dst.Keystore[0].Alias, "partner_ca"))
	require.Len(t, dst.AccessPolicies, 1)
	assert.Len(t, dst.AccessPolicies[0].References, 1)
	assert.Equal(t, "4711", dst.NumberRanges["Invoice"]["CurrentValue"])
	require.NotNil(t, dst.Artifacts["A"].Runtime, "deployed again")
	assert.Equal(t, "STARTED", dst.Artifacts["A"].Runtime.Status)
	assert.Nil(t, dst.Artifacts["Lib"].Runtime, "was not running at backup time")

	// a second restore changes nothing; a credential whose secret is given
	// is written again (the tenant cannot tell whether it is the same)
	again, err := RestoreBackup(context.Background(), dst.Executer(), archive, opts)
	require.NoError(t, err)
	for _, s := range again.Steps {
		if s.Area == RestoreCredentials && s.Object == "ERP_User" {
			assert.Equal(t, RestoreUpdate, s.Action)
			continue
		}
		assert.Contains(t, []string{RestoreUnchanged, RestoreManual, RestoreInfo}, s.Action, "%s %s: %s %s", s.Area, s.Object, s.Action, s.Detail)
	}
}

// A restore into the tenant itself after changes: changed content and
// parameters go back, a counter that moved on is kept, nothing is deleted.
func TestRestoreAfterChanges(t *testing.T) {
	m := restoreSource(t)
	archive := filepath.Join(t.TempDir(), "b.tar.gz")
	_, err := CreateBackup(context.Background(), m.Executer(), "source", BackupOptions{Output: archive})
	require.NoError(t, err)

	m.Artifacts["A"].Parameters["Host"] = "wrong.example.com"
	m.NumberRanges["Invoice"]["CurrentValue"] = "5000" // numbers issued since: never go back
	m.PDStrings["ACME/Extra"] = "kept"
	m.Artifacts["A"].Zip = zipOf(t, map[string]string{"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\nBundle-SymbolicName: A\n",
		"src/main/resources/parameters.prop": "Host=erp.example.com\nPort=443\n", "src/main/resources/script/added.groovy": "// changed since the backup\n"})

	res, err := RestoreBackup(context.Background(), m.Executer(), archive, RestoreOptions{Areas: []string{RestoreArtifacts, RestoreParameters, RestorePartnerDirectory, RestoreNumberRanges},
		Deploy: Options{Interval: time.Millisecond, MaxChecks: 50}})
	require.NoError(t, err)
	assert.Equal(t, RestoreUpdate, steps(res, RestoreArtifacts)["A"])
	assert.Equal(t, RestoreUpdate, steps(res, RestoreParameters)["A"])
	assert.Equal(t, "erp.example.com", m.Artifacts["A"].Parameters["Host"])
	assert.Equal(t, RestoreUnchanged, steps(res, RestoreNumberRanges)["Invoice"])
	assert.Equal(t, "5000", m.NumberRanges["Invoice"]["CurrentValue"])
	assert.Equal(t, "kept", m.PDStrings["ACME/Extra"], "nothing is deleted")
	assert.NotContains(t, res.Areas, RestoreDeploy)
}

func TestRestoreRefusesDamagedArchive(t *testing.T) {
	m := backupTenant(t)
	archive := filepath.Join(t.TempDir(), "b.tar.gz")
	_, err := CreateBackup(context.Background(), m.Executer(), "source", BackupOptions{Output: archive})
	require.NoError(t, err)
	data, err := os.ReadFile(archive)
	require.NoError(t, err)
	data[len(data)/2] ^= 0xff
	require.NoError(t, os.WriteFile(archive, data, 0o600))
	_, err = RestoreBackup(context.Background(), m.Executer(), archive, RestoreOptions{DryRun: true})
	require.Error(t, err)

	_, err = RestoreBackup(context.Background(), m.Executer(), archive, RestoreOptions{DryRun: true, Areas: []string{"everything"}})
	assert.ErrorContains(t, err, "unknown restore area")
}
