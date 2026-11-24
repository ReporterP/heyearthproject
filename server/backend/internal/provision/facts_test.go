package provision

import (
	"strings"
	"testing"
	"time"
)

func TestFactsParse(t *testing.T) {
	out := "KERNEL=6.8.0-49-generic\nBOOT=1795400000\nREBOOT=1\nSECURITY=7\nSSHPW=0\nDISK=63\n"
	f, err := ParseFacts(out)
	if err != nil {
		t.Fatal(err)
	}
	if f.Kernel != "6.8.0-49-generic" || !f.BootAt.Equal(time.Unix(1795400000, 0)) || !f.RebootRequired ||
		f.SecurityUpdates != 7 || f.SSHPassword || f.DiskPct != 63 {
		t.Fatalf("parsed: %+v", f)
	}
	bad := []string{
		"",
		"garbage\n",
		strings.Replace(out, "KERNEL=6.8.0-49-generic", "KERNEL=", 1),
		strings.Replace(out, "BOOT=1795400000", "BOOT=yesterday", 1),
		strings.Replace(out, "REBOOT=1", "REBOOT=maybe", 1),
		strings.Replace(out, "SECURITY=7", "SECURITY=-1", 1),
		strings.Replace(out, "SSHPW=0", "", 1),
		strings.Replace(out, "DISK=63", "DISK=163", 1),
	}
	for i, b := range bad {
		if _, err := ParseFacts(b); err == nil {
			t.Errorf("#%d garbage must be an error, not zeros: %q", i, b)
		}
	}
}

func TestFactsScriptIsReadOnly(t *testing.T) {
	for _, w := range []string{"apt-get install", "apt-get upgrade -y", "rm ", "systemctl restart", "systemctl reboot", "> /", "tee "} {
		if strings.Contains(FactsScript, w) {
			t.Errorf("the facts script must only read, found %q", w)
		}
	}
	if !strings.Contains(FactsScript, "apt-get -s ") {
		t.Error("security updates are counted with a simulation (apt-get -s)")
	}
}

func TestFactsRecipe(t *testing.T) {
	n := Node{ID: 3, Role: "exit", Lifecycle: "online", HostKeyConfirmed: true}
	if _, err := RecipeFor(KindFacts, n, &State{}); err == nil {
		t.Fatal("without a Record the facts have nowhere to go")
	}
	st := &State{Facts: &FactsOptions{Record: nil}}
	if _, err := RecipeFor(KindFacts, n, st); err == nil {
		t.Fatal("Record is required")
	}
	rc, err := RecipeFor(KindFacts, n, &State{Facts: &FactsOptions{Record: func(NodeFacts) error { return nil }}})
	if err != nil || len(rc) != 1 || rc[0].Apply != nil || rc[0].Verify == nil {
		t.Fatalf("one read-only step (Verify only): %v %+v", err, rc)
	}
	n.HostKeyConfirmed = false
	if _, err := RecipeFor(KindFacts, n, &State{Facts: &FactsOptions{Record: func(NodeFacts) error { return nil }}}); err == nil {
		t.Fatal("never on an unconfirmed host key")
	}
}

func TestRebootAndAgentRestartRecipes(t *testing.T) {
	n := Node{ID: 5, Role: "relay", Lifecycle: "online", HostKeyConfirmed: true}
	rc, err := RecipeFor(KindReboot, n, &State{})
	if err != nil || len(rc) != 1 || rc[0].Apply == nil || rc[0].Verify != nil || rc[0].Rollback != nil {
		t.Fatalf("reboot: one Apply, nothing to verify or undo: %v %+v", err, rc)
	}
	if !strings.Contains(rebootCmd, "systemd-run --on-active=") || !strings.Contains(rebootCmd, "systemctl reboot") {
		t.Fatalf("the reboot is delayed so the SSH session ends first: %q", rebootCmd)
	}
	if _, err := RecipeFor(KindAgentRestart, n, &State{}); err == nil {
		t.Fatal("node-agent lives on exits only")
	}
	n.Role = "exit"
	rc, err = RecipeFor(KindAgentRestart, n, &State{})
	if err != nil || len(rc) != 1 || rc[0].Apply == nil || rc[0].Verify != nil {
		t.Fatalf("agentrestart: restart and check in one Apply, no Verify (an active but mute agent is restarted too): %v %+v", err, rc)
	}
	for _, k := range []string{KindFacts, KindReboot, KindAgentRestart} {
		if !ValidKind(k) {
			t.Errorf("%s is a job kind", k)
		}
	}
	n.Lifecycle = "offline"
	if _, err := RecipeFor(KindReboot, n, &State{}); err == nil {
		t.Fatal("only a working node is rebooted from the console")
	}
}
