package hubnet

import (
	"net/netip"
	"os/exec"
	"strings"
	"testing"
)

var (
	curIP = netip.MustParseAddr("192.0.2.10")
	newIP = netip.MustParseAddr("198.51.100.20")
	gwIP  = netip.MustParseAddr("203.0.113.1")
)

func hubRoute() DefaultRoute {
	return DefaultRoute{Gateway: gwIP, Dev: "ens3", Src: curIP, Metric: 100, Proto: "static", Onlink: true}
}

// bashSyntax fails the test when script is not valid bash (skipped without bash).
func bashSyntax(t *testing.T, script string) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	cmd := exec.Command(bash, "-n")
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n: %v\n%s\n%s", err, out, script)
	}
}

func TestParseDefaultRoute(t *testing.T) {
	r, err := ParseDefaultRoute([]byte(`[{"dst":"default","gateway":"203.0.113.1","dev":"ens3","protocol":"static","prefsrc":"192.0.2.10","metric":100,"flags":["onlink"]}]`))
	if err != nil || r != hubRoute() {
		t.Fatalf("got %+v, %v", r, err)
	}
	for name, in := range map[string]string{
		"none":       `[]`,
		"two":        `[{"dst":"default","gateway":"203.0.113.1","dev":"ens3"},{"dst":"default","gateway":"203.0.113.2","dev":"ens4"}]`,
		"junk":       `{`,
		"no gateway": `[{"dst":"default","dev":"ens3"}]`,
	} {
		if _, err := ParseDefaultRoute([]byte(in)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestRouteReplaceScript(t *testing.T) {
	s := RouteReplaceScript(hubRoute(), newIP)
	if !strings.Contains(s, "ip route replace default via 203.0.113.1 dev ens3 src 198.51.100.20 metric 100 proto static onlink") {
		t.Fatalf("route keeps its attributes:\n%s", s)
	}
	bashSyntax(t, s)
}

func TestAddrScripts(t *testing.T) {
	add := AddrAddScript("ens3", newIP)
	if !strings.Contains(add, "ip addr add 198.51.100.20/32 dev ens3") {
		t.Fatalf("add:\n%s", add)
	}
	del := AddrDelScript("ens3", newIP)
	if !strings.Contains(del, "ip addr del") || !strings.Contains(del, "198.51.100.20/") {
		t.Fatalf("del:\n%s", del)
	}
	bashSyntax(t, add)
	bashSyntax(t, del)
}

func TestNetplanWriteScript(t *testing.T) {
	s := NetplanWriteScript("/etc/netplan/01-netcfg.yaml", []byte("network: {}\n"), "/etc/netplan/01-netcfg.yaml.bak-hubaddr-7")
	for _, want := range []string{"base64 -d", "netplan generate", "/etc/netplan/01-netcfg.yaml.bak-hubaddr-7", "chmod 600"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "netplan apply") {
		t.Error("never netplan apply")
	}
	bashSyntax(t, s)
}

func TestRevertScriptRestoresSourceAndSetting(t *testing.T) {
	s := RevertScript(RevertParams{ChangeID: 7, Route: hubRoute(), OldSrc: curIP, OldMain: "192.0.2.10", Marker: "/opt/heyearth/state/hubaddr/7-reverted"})
	for _, want := range []string{
		"ip route replace default via 203.0.113.1 dev ens3 src 192.0.2.10 metric 100 proto static onlink",
		"main_server_host",
		"'192.0.2.10'",
		"config_version",
		"hub_addr_transition_since",
		"hysteria2-client-*",
		"heyearth-stls-client-*",
		"touch /opt/heyearth/state/hubaddr/7-reverted",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q:\n%s", want, s)
		}
	}
	bashSyntax(t, s)

	q := RevertScript(RevertParams{ChangeID: 7, Route: hubRoute(), OldSrc: curIP, OldMain: "a'b", Marker: "/tmp/m"})
	if !strings.Contains(q, "a''b") {
		t.Errorf("a quote in the value is SQL-escaped:\n%s", q)
	}
	bashSyntax(t, q)
}

func TestTimerScripts(t *testing.T) {
	arm := ArmTimerScript("heyearth-hubaddr-revert-7", "/opt/heyearth/state/hubaddr/7-revert.sh", 600)
	for _, want := range []string{"systemd-run", "--unit=heyearth-hubaddr-revert-7", "--on-active=600s", "/opt/heyearth/state/hubaddr/7-revert.sh"} {
		if !strings.Contains(arm, want) {
			t.Errorf("arm missing %q:\n%s", want, arm)
		}
	}
	dis := DisarmTimerScript("heyearth-hubaddr-revert-7")
	if !strings.Contains(dis, "systemctl stop heyearth-hubaddr-revert-7.timer") || strings.Contains(dis, "stop heyearth-hubaddr-revert-7.service") {
		t.Errorf("disarm stops the timer only (a running revert is never killed):\n%s", dis)
	}
	bashSyntax(t, arm)
	bashSyntax(t, dis)
	bashSyntax(t, RestartTunnelsScript())
}

func TestCounterScripts(t *testing.T) {
	s := CounterTableScript(curIP)
	for _, want := range []string{"inet heyearth_hubaddr", "ip daddr 192.0.2.10", "ct state new", "443", "2443", "30000-65000", "old_new"} {
		if !strings.Contains(s, want) {
			t.Errorf("counter table missing %q:\n%s", want, s)
		}
	}
	bashSyntax(t, s)
	bashSyntax(t, CounterReadScript())
	bashSyntax(t, CounterDropScript())
	n, err := ParseCounter("table inet heyearth_hubaddr {\n\tcounter old_new {\n\t\tpackets 42 bytes 2520\n\t}\n}\n")
	if err != nil || n != 42 {
		t.Fatalf("ParseCounter = %d, %v", n, err)
	}
	if _, err := ParseCounter("Error: No such file or directory"); err == nil {
		t.Error("no table: expected an error")
	}
}

func TestNodeSSHAllowScript(t *testing.T) {
	s := NodeSSHAllowScript(22, []netip.Addr{curIP, netip.MustParseAddr("203.0.113.5")}, newIP)
	for _, want := range []string{
		"ufw allow from 198.51.100.20 to any port 22 proto tcp",
		"iptables -I INPUT -p tcp -s 198.51.100.20 --dport 22 -j ACCEPT",
		"SSH_ALLOW_ADDED", "SSH_ALLOW_NOT_NEEDED",
		"192.0.2.10", "203.0.113.5",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q:\n%s", want, s)
		}
	}
	bashSyntax(t, s)
}
