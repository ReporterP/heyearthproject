package main

import "testing"

// The Hub's Xray must not write an access log. Without "access", Xray writes one line per
// connection (the person's address, the site and port, the account tag) to stdout, which is
// the systemd journal: thousands of lines in minutes, read by nothing, and against the privacy
// policy («без адресов сайтов»). Found while answering the lawyer's checklist, 2026-11-18.
func TestXrayConfigHasNoAccessLog(t *testing.T) {
	b, err := buildXrayConfig(baseInputs(), quotaCutoff{})
	if err != nil {
		t.Fatal(err)
	}
	logCfg, ok := b.config["log"].(map[string]string)
	if !ok {
		t.Fatalf("log section has an unexpected shape: %#v", b.config["log"])
	}
	if logCfg["access"] != "none" {
		t.Errorf(`log.access = %q, want "none" (no per-connection log)`, logCfg["access"])
	}
	if logCfg["loglevel"] != "warning" {
		t.Errorf(`log.loglevel = %q, want "warning"`, logCfg["loglevel"])
	}
	if logCfg["dnsLog"] == "true" {
		t.Error("dnsLog would log every name a person looks up")
	}
}
