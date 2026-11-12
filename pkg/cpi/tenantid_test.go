package cpi

import "testing"

// TenantID names a tenant the same way for a host and for a cpictl gateway
// URL (cpicli#86): a gateway names its SAP tenant by the path.
func TestTenantID(t *testing.T) {
	for in, want := range map[string]string{
		"mytenant.it-cpi018.cfapps.eu10.hana.ondemand.com": "mytenant.it-cpi018.cfapps.eu10.hana.ondemand.com",
		"https://mytenant.example.com/":                    "mytenant.example.com",
		"https://mytenant.example.com:8443":                "mytenant.example.com:8443",
		"http://127.0.0.1:38081":                           "127.0.0.1:38081",
		"https://gw.example.com/t/dev":                     "gw.example.com/t/dev",
		"https://gw.example.com/t/dev/":                    "gw.example.com/t/dev",
		"https://gw.example.com:9443/t/prod":               "gw.example.com:9443/t/prod",
		"":                                                 "",
	} {
		if got := TenantID(in); got != want {
			t.Errorf("TenantID(%q) = %q, want %q", in, got, want)
		}
	}
	sd := &ServiceDetails{Host: "h.example.com", Gateway: "https://gw.example.com/t/dev"}
	if sd.Target() != sd.Gateway {
		t.Errorf("gateway mode targets the gateway")
	}
	sd.Gateway = ""
	if sd.Target() != "h.example.com" {
		t.Errorf("else the host")
	}
}
