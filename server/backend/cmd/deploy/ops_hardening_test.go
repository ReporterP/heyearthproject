package main

import (
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
