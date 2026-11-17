package main

import (
	"strings"
	"testing"
)

func TestDDoSNftObserveNeverDrops(t *testing.T) {
	nft := renderDDoSNft("observe", defaultDDoSLimits)
	if strings.Contains(nft, " drop") {
		t.Fatalf("observe mode must not drop:\n%s", nft)
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
