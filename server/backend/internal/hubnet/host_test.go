package hubnet

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestParseIfaceAddrs(t *testing.T) {
	ps, err := ParseIfaceAddrs([]byte(`[{"ifindex":2,"ifname":"ens3","addr_info":[{"family":"inet","local":"203.0.113.5","prefixlen":24},{"family":"inet","local":"192.0.2.10","prefixlen":32},{"family":"inet6","local":"2001:db8::1","prefixlen":64}]}]`))
	if err != nil || len(ps) != 2 || ps[0].String() != "203.0.113.5/24" || ps[1].String() != "192.0.2.10/32" {
		t.Fatalf("got %v, %v", ps, err)
	}
	if _, err := ParseIfaceAddrs([]byte(`[]`)); err == nil {
		t.Error("no interface: want an error")
	}
}

func TestNetplanFilesPick(t *testing.T) {
	other := "network:\n  version: 2\n  ethernets:\n    ens4:\n      addresses:\n        - 192.0.2.200/24\n"
	out := "=== /etc/netplan/01-netcfg.yaml\n" + base64.StdEncoding.EncodeToString([]byte(hubNetplan)) + "\n" +
		"=== /etc/netplan/99-other.yaml\n" + base64.StdEncoding.EncodeToString([]byte(other)) + "\n"
	files, err := ParseNetplanFiles(out)
	if err != nil || len(files) != 2 || string(files["/etc/netplan/01-netcfg.yaml"]) != hubNetplan {
		t.Fatalf("parse: %v %v", files, err)
	}
	if p, err := PickNetplanFile(files, "ens3"); err != nil || p != "/etc/netplan/01-netcfg.yaml" {
		t.Fatalf("pick: %q %v", p, err)
	}
	files["/etc/netplan/50-cloud-init.yaml"] = []byte(hubNetplan)
	if _, err := PickNetplanFile(files, "ens3"); err == nil || !strings.Contains(err.Error(), "50-cloud-init") {
		t.Fatalf("two files define ens3: want a refusal naming them, got %v", err)
	}
	if _, err := PickNetplanFile(map[string][]byte{"/etc/netplan/x.yaml": []byte(other)}, "ens3"); err == nil {
		t.Fatal("no file defines ens3: want an error")
	}
	if !strings.Contains(NetplanFilesScript(), "base64 -w0") {
		t.Fatal("files are listed base64-encoded")
	}
	bashSyntax(t, NetplanFilesScript())
}
