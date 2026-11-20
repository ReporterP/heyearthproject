package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDDoSNftObserveNeverDrops(t *testing.T) {
	nft := renderDDoSNft("observe", defaultDDoSLimits)
	for _, l := range strings.Split(nft, "\n") {
		// The only drop in observe mode: strangers on the entries' PROXY port.
		if strings.Contains(l, " drop") && !strings.Contains(l, "tcp dport 8447 counter name not_relay_8447 drop") {
			t.Fatalf("observe mode must not drop: %s", l)
		}
	}
	if !strings.Contains(nft, "mode observe.") {
		t.Fatalf("header must carry the mode (hardeningState parses it):\n%s", nft)
	}
}

func TestDDoSNftEnforce(t *testing.T) {
	nft := renderDDoSNft("enforce", defaultDDoSLimits)
	for _, want := range []string{
		"counter name over_tcp drop", "counter name over_udp drop", "counter name over_ssh drop",
		"ip saddr @allow4 return", "ip6 saddr @allow6 return", "ct direction reply return", `iif "lo" return`,
	} {
		if !strings.Contains(nft, want) {
			t.Errorf("enforce table lacks %q", want)
		}
	}
	// The calibration meters never drop, and the table never accepts (ufw keeps deciding).
	for _, l := range strings.Split(nft, "\n") {
		if strings.Contains(l, "_lo") && strings.Contains(l, "drop") {
			t.Errorf("calibration meter drops: %s", l)
		}
		if strings.Contains(l, " accept") && !strings.Contains(l, "policy accept") {
			t.Errorf("the flood table must never accept: %s", l)
		}
	}
	// The allow / reply / loopback exemptions come before any limit.
	if strings.Index(nft, "@allow4 return") > strings.Index(nft, "update @") {
		t.Error("allow set must be checked before the meters")
	}
}

func TestHardeningScriptsParse(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	for name, s := range map[string]string{
		"sysctl":     renderSysctlScript(),
		"unattended": renderUnattendedScript(),
		"sshd":       renderSSHDApply(renderSSHDConf(true)),
		"sshdRb":     sshdRollback,
		"ddos":       renderDDoSApply(renderDDoSNft("enforce", defaultDDoSLimits)),
		"ddosRb":     ddosRollback,
		"allow":      ddosAllowScript,
		"nginx":      renderNginxLimitsApply("example.org"),
		"nginxRb":    nginxLimitsRollback,
		"f2bTrusted": f2bTrustedScript,
	} {
		if out, code := runBashStdin(t, s, "-n"); code != 0 {
			t.Errorf("%s: bash -n failed (%d): %s", name, code, out)
		}
	}
}

func TestSSHDConfPasswordSwitch(t *testing.T) {
	if strings.Contains(renderSSHDConf(false), "PasswordAuthentication") {
		t.Error("password logins must stay as they are unless --ssh-password off")
	}
	off := renderSSHDConf(true)
	for _, want := range []string{"PasswordAuthentication no", "PermitRootLogin prohibit-password", "PerSourceMaxStartups 3"} {
		if !strings.Contains(off, want) {
			t.Errorf("password-off drop-in lacks %q", want)
		}
	}
}

func TestNodeHardeningScriptParses(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	for _, jail := range []bool{true, false} {
		s := nodeSSHFunc + "nodessh 2 <<'HEYEARTH_NODE_EOF'\n" + renderNodeHardeningScript(jail, 22, true) + "HEYEARTH_NODE_EOF\n"
		if out, code := runBashStdin(t, s, "-n"); code != 0 {
			t.Errorf("jail=%v: bash -n failed (%d): %s", jail, code, out)
		}
		if strings.Contains(renderNodeHardeningScript(jail, 22, false), "HEYEARTH_NODE_EOF") {
			t.Error("the node script must not contain the heredoc terminator")
		}
	}
}

func TestVhostHeaderIncludes(t *testing.T) {
	v := buildNginxConfSSL("example.org", "/etc/letsencrypt/live/example.org", true)
	if strings.Count(v, "include /etc/nginx/heyearth-headers/site.conf;") != 4 || strings.Count(v, "include /etc/nginx/heyearth-headers/admin.conf;") != 3 {
		t.Fatalf("header includes:\n%s", v)
	}
	old := vhostWithoutHeaders(v)
	if strings.Contains(old, "heyearth-headers") || !strings.Contains(old, `add_header X-Content-Type-Options "nosniff" always;`) {
		t.Fatal("vhostWithoutHeaders must give the pre-header vhost back")
	}
	if !haveBash() {
		return
	}
	if out, code := runBashStdin(t, renderHeadersApply("example.org"), "-n"); code != 0 {
		t.Fatalf("bash -n: %s", out)
	}
}

func TestVhost429AndPreviousVersions(t *testing.T) {
	v := buildNginxConfSSL("example.org", "/etc/letsencrypt/live/example.org", true)
	if strings.Count(v, "error_page 429 /429.html;") != 2 || strings.Count(v, "error_page 429 = @he429;") != 2 || strings.Count(v, "location @he429 ") != 2 {
		t.Fatalf("429 handling in both servers and both API locations:\n%s", v)
	}
	no429 := vhostWithout429(v)
	if strings.Contains(no429, "429") {
		t.Fatal("vhostWithout429 left 429 lines")
	}
	if !strings.Contains(previousVhostShas(v), sha256hex(no429)) || !strings.Contains(previousVhostShas(v), sha256hex(vhostWithoutHeaders(no429))) {
		t.Fatal("previous versions")
	}
}

// The entries' GRE tunnels: the far end of every he-r<id> device joins allow4 (through the tunnel
// an entry connects from it), other point-to-point peers do not.
func TestDDoSAllowScriptTunnelPeers(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	stubs := map[string]string{
		"runuser": `echo 192.0.2.2; echo 198.51.100.42`,
		"ip": `cat <<'IPEOF'
2: ens3    inet 203.0.113.10/32 scope global ens3
7: he-r6    inet 10.77.0.25 peer 10.77.0.26/32 scope global he-r6
8: wg0    inet 10.9.0.1 peer 10.9.0.2/32 scope global wg0
IPEOF`,
		"nft": `case "$*" in "list table inet heyearth_ddos") exit 0 ;; "-f -") cat > "$OUT" ;; esac`,
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/usr/bin/env bash\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(dir, "nft.in")
	script := "export PATH=" + shQuote(shellDir(dir)) + ":\"$PATH\" OUT=" + shQuote(shellDir(out)) + "\n" + ddosAllowScript
	if o, code := runBashStdin(t, script); code != 0 {
		t.Fatalf("allow script: %d %s", code, o)
	}
	b, _ := os.ReadFile(out)
	got := string(b)
	for _, want := range []string{"10.77.0.26", "10.77.0.25", "192.0.2.2", "198.51.100.42", "203.0.113.10"} {
		if !strings.Contains(got, want) {
			t.Errorf("allow4 lacks %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, "10.9.0.2") {
		t.Errorf("a foreign point-to-point peer was allowed:\n%s", got)
	}
}

// The entries' tunnels: their devices are exempt like the entries' addresses, and the tunnel pool
// arriving on any other interface is dropped before the allow set could let it through.
func TestDDoSNftTunnelDevices(t *testing.T) {
	for _, mode := range []string{"observe", "enforce"} {
		nft := renderDDoSNft(mode, defaultDDoSLimits)
		spoofRule := "ip saddr 10.77.0.0/16 counter name spoof_tunnel"
		if mode == "enforce" {
			spoofRule += " drop"
		}
		dev, spoof, allow := strings.Index(nft, `iifname "he-r*" return`), strings.Index(nft, spoofRule+"\n"), strings.Index(nft, "ip saddr @allow4 return")
		if dev < 0 || spoof < 0 || !(dev < spoof && spoof < allow) {
			t.Errorf("%s: tunnel rules missing or out of order (dev %d, spoof %d, allow %d)", mode, dev, spoof, allow)
		}
	}
}
