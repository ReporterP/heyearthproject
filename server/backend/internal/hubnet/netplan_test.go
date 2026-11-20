package hubnet

import (
	"net/netip"
	"strings"
	"testing"
)

// A netplan file shaped like the Hub's (RFC 5737 addresses): the gateway carrier first, the
// address users reach today as a /32, the default route sourced from it.
const hubNetplan = `# This file is generated from information provided by the datasource.
network:
  version: 2
  ethernets:
    ens3:
      addresses:
        - 203.0.113.5/24   # primary, carries the gateway
        - 192.0.2.10/32
      routes:
        - to: default
          via: 203.0.113.1
          from: 192.0.2.10
      nameservers:
        addresses: [1.1.1.1, 8.8.8.8]
`

func TestNetplanAddAddress(t *testing.T) {
	out, changed, err := NetplanAddAddress([]byte(hubNetplan), "ens3", "198.51.100.20/32")
	if err != nil || !changed {
		t.Fatalf("add: changed=%v err=%v", changed, err)
	}
	want := strings.Replace(hubNetplan, "        - 192.0.2.10/32\n", "        - 192.0.2.10/32\n        - 198.51.100.20/32\n", 1)
	if string(out) != want {
		t.Fatalf("only one line is inserted, the rest stays byte for byte:\n%s", out)
	}
	again, changed, err := NetplanAddAddress(out, "ens3", "198.51.100.20/32")
	if err != nil || changed || string(again) != string(out) {
		t.Fatalf("second add is a no-op: changed=%v err=%v", changed, err)
	}
}

func TestNetplanAddAddressRefusesUnexpectedLayouts(t *testing.T) {
	for name, doc := range map[string]string{
		"flow list":    strings.Replace(hubNetplan, "      addresses:\n        - 203.0.113.5/24   # primary, carries the gateway\n        - 192.0.2.10/32\n", "      addresses: [203.0.113.5/24, 192.0.2.10/32]\n", 1),
		"no addresses": strings.Replace(hubNetplan, "      addresses:\n        - 203.0.113.5/24   # primary, carries the gateway\n        - 192.0.2.10/32\n", "", 1),
		"other iface":  strings.Replace(hubNetplan, "    ens3:", "    eth0:", 1),
		"not yaml":     "network: [",
		"empty":        "",
	} {
		if _, _, err := NetplanAddAddress([]byte(doc), "ens3", "198.51.100.20/32"); err == nil {
			t.Errorf("%s: expected a refusal", name)
		}
	}
}

func TestNetplanRemoveAddress(t *testing.T) {
	out, changed, err := NetplanRemoveAddress([]byte(hubNetplan), "ens3", "192.0.2.10/32")
	if err != nil || !changed {
		t.Fatalf("remove: changed=%v err=%v", changed, err)
	}
	if string(out) != strings.Replace(hubNetplan, "        - 192.0.2.10/32\n", "", 1) {
		t.Fatalf("only that line goes:\n%s", out)
	}
	if _, changed, err := NetplanRemoveAddress(out, "ens3", "192.0.2.10/32"); err != nil || changed {
		t.Fatalf("absent address: changed=%v err=%v", changed, err)
	}
}

func TestNetplanSetDefaultFrom(t *testing.T) {
	src := netip.MustParseAddr("198.51.100.20")
	out, changed, err := NetplanSetDefaultFrom([]byte(hubNetplan), "ens3", src)
	if err != nil || !changed {
		t.Fatalf("set from: changed=%v err=%v", changed, err)
	}
	if string(out) != strings.Replace(hubNetplan, "from: 192.0.2.10", "from: 198.51.100.20", 1) {
		t.Fatalf("only the from value changes:\n%s", out)
	}

	noFrom := strings.Replace(hubNetplan, "          from: 192.0.2.10\n", "", 1)
	out, changed, err = NetplanSetDefaultFrom([]byte(noFrom), "ens3", src)
	if err != nil || !changed || string(out) != strings.Replace(noFrom, "          via: 203.0.113.1\n", "          via: 203.0.113.1\n          from: 198.51.100.20\n", 1) {
		t.Fatalf("from inserted after the route's last key: changed=%v err=%v\n%s", changed, err, out)
	}

	two := strings.Replace(hubNetplan, "      nameservers:", "        - to: 0.0.0.0/0\n          via: 203.0.113.1\n      nameservers:", 1)
	if _, _, err := NetplanSetDefaultFrom([]byte(two), "ens3", src); err == nil {
		t.Error("two default routes: expected a refusal")
	}
	none := strings.Replace(hubNetplan, "to: default", "to: 10.0.0.0/8", 1)
	if _, _, err := NetplanSetDefaultFrom([]byte(none), "ens3", src); err == nil {
		t.Error("no default route: expected a refusal")
	}
}

// The Hub's real shape (2026-11-20): a policy-routing default route in table 2 next to the main
// table's. Only the main table's default route (no `table:`, or table main/254) gets `from:`.
const hubNetplanTable2 = `network:
  version: 2
  ethernets:
    ens3:
      addresses:
        - 203.0.113.5/24
        - 192.0.2.99/24
        - 192.0.2.10/32
      routes:
        - to: default
          via: 203.0.113.1
          on-link: true
          from: 192.0.2.10
        - to: default
          via: 203.0.113.1
          on-link: true
          table: 2
      routing-policy:
        - from: 192.0.2.99
          table: 2
`

func TestNetplanSetDefaultFromIgnoresOtherTables(t *testing.T) {
	src := netip.MustParseAddr("198.51.100.20")
	out, changed, err := NetplanSetDefaultFrom([]byte(hubNetplanTable2), "ens3", src)
	if err != nil || !changed || string(out) != strings.Replace(hubNetplanTable2, "from: 192.0.2.10", "from: 198.51.100.20", 1) {
		t.Fatalf("only the main table's route changes: changed=%v err=%v\n%s", changed, err, out)
	}
	noFrom := strings.Replace(hubNetplanTable2, "          from: 192.0.2.10\n", "", 1)
	out, changed, err = NetplanSetDefaultFrom([]byte(noFrom), "ens3", src)
	want := strings.Replace(noFrom, "          on-link: true\n        - to: default", "          on-link: true\n          from: 198.51.100.20\n        - to: default", 1)
	if err != nil || !changed || string(out) != want {
		t.Fatalf("from inserted into the main table's route only: changed=%v err=%v\n%s", changed, err, out)
	}
	main254 := strings.Replace(hubNetplanTable2, "          table: 2\n      routing", "          table: 254\n      routing", 1)
	if _, _, err := NetplanSetDefaultFrom([]byte(main254), "ens3", src); err == nil {
		t.Fatal("table 254 is the main table: two main default routes must be refused")
	}
}
