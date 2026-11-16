package config

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// teardownStub is systemctlStub with what the teardown scripts rely on. A unit exists when
// ./etc/systemd/system/<unit>.service does; disable/stop of a missing unit fail as systemd does
// and change nothing. ./enabled.<unit> = enabled: disable removes it (with --now it also stops
// the unit); stop deactivates; STUCK=<unit> keeps that unit running whatever is asked. Every
// disable/stop is logged to ./calls. nft keeps tables as ./nft.<table>; NFT_STUCK=1 makes
// `delete table` fail and keep the table.
const teardownStub = systemctlStub + `eval "base_$(declare -f systemctl)"
systemctl() {
  local u=${@: -1}
  u=${u%.service}
  case "$1" in
    disable|stop)
      echo "$*" >> ./calls
      [ -f "./etc/systemd/system/$u.service" ] || { echo "Failed to $1 $u.service: Unit $u.service not loaded." >&2; return 5; }
      if [ "$1" = disable ]; then rm -f "./enabled.$u"; fi
      if [ "$1" = stop ] || [ "$2" = --now ]; then [ "$u" = "${STUCK:-}" ] || rm -f "./active.$u"; fi ;;
    is-enabled) if [ -f "./enabled.$u" ]; then echo enabled; else echo disabled; return 1; fi ;;
    *) base_systemctl "$@" ;;
  esac
}
nft() {
  echo "nft $*" >> ./nft.log
  case "$1 $2" in
    "delete table") [ -f "./nft.$4" ] || { echo "Error: No such file or directory" >&2; return 1; }
      [ "${NFT_STUCK:-0}" = 1 ] && return 1
      rm -f "./nft.$4" ;;
    "list table") [ -f "./nft.$4" ] || return 1; cat "./nft.$4" ;;
  esac
}
pkill() { echo "$*" >> ./pkill.log; }
`

// unit installs a unit file in dir; active/enabled create the stub's state files.
func unit(t *testing.T, dir, name string, active, enabled bool) {
	t.Helper()
	mustWrite(t, dir, "etc/systemd/system/"+name+".service", "[Unit]\n", 0o644)
	if active {
		mustWrite(t, dir, "active."+name, "", 0o644)
	}
	if enabled {
		mustWrite(t, dir, "enabled."+name, "", 0o644)
	}
}

func TestExitStopServicesUsesRealUnitNames(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	if strings.Contains(ExitStopServicesScript, "hysteria2-server") || strings.Contains(ExitStopServicesScript, "hysteria-server") {
		t.Fatal("no hysteria2-server / hysteria-server unit exists on the exits")
	}
	dir := t.TempDir()
	for _, u := range []string{"hysteria2", ExitSTLSUnit, "xray", "node-agent"} {
		unit(t, dir, u, true, true)
	}
	// mtproxy was never installed (MTProxy switched off); the Hub-only guard of node-agent and the
	// inactive nftables.service must be left alone.
	unit(t, dir, ExitAgentGuardService, true, true)
	unit(t, dir, "nftables", false, false)
	for run := 0; run < 2; run++ {
		out, err := shellRun(t, dir, teardownStub, ExitStopServicesScript)
		if err != nil {
			t.Fatalf("run %d: %v\n%s", run, err, out)
		}
	}
	for _, u := range []string{"hysteria2", ExitSTLSUnit, "xray", "node-agent"} { // the real unit names
		if exists(dir, "active."+u) || exists(dir, "enabled."+u) {
			t.Errorf("%s still running or enabled", u)
		}
	}
	if !exists(dir, "active."+ExitAgentGuardService) || !exists(dir, "enabled."+ExitAgentGuardService) ||
		strings.Contains(readRel(dir, "calls"), "agent-guard") || regexp.MustCompile(`(?m) nftables$`).MatchString(readRel(dir, "calls")) {
		t.Fatalf("touched what it must not:\n%s", readRel(dir, "calls"))
	}
	if got := ExitServiceUnits(); strings.Join(got, " ") != "hysteria2 heyearth-stls xray node-agent mtproxy heyearth-tg-mtproxy heyearth-tg-mtproxy-refresh.timer" {
		t.Fatalf("units %v", got)
	}
}

func TestExitStopServicesFailsWhileAUnitRuns(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	for _, u := range ExitServiceUnits() {
		unit(t, dir, u, true, true)
	}
	out, err := shellRun(t, dir, teardownStub, ExitStopServicesScript, "STUCK=xray")
	if err == nil || !strings.Contains(out, "xray") {
		t.Fatalf("a running xray must fail the step: %v\n%s", err, out)
	}
}

func TestExitRotatePasswordRewritesBothFiles(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	const oldPW, newPW = "b2xkLXBhc3N3b3JkLXh4eA==", "bmV3LXBhc3N3b3JkLXl5eQ=="
	keys, _ := json.MarshalIndent(ExitRealityKeys{PrivateKey: "priv", PublicKey: "pub", ShortID: "ab", UUID: "u", SSPassword: oldPW}, "", "  ")
	yaml := ExitHysteriaServerYAML(oldPW, "203.0.113.7", 8443) + "obfs:\n  type: salamander\n  salamander:\n    password: keep-me\n"
	for name, k := range map[string]string{"indented": string(keys) + "\n", "compact": `{"privateKey":"priv","ssPassword":"` + oldPW + `","uuid":"u"}`} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			mustWrite(t, dir, rel(ExitHysteriaConfig), yaml, 0o600)
			mustWrite(t, dir, rel(ExitHysteriaConfig)+".bak", "  password: "+oldPW+"\n", 0o600)
			mustWrite(t, dir, rel(ExitKeysPath), k, 0o600)
			mustWrite(t, dir, rel(ExitKeysPath)+".bak", oldPW, 0o600)
			out, err := shellRun(t, dir, "", ExitRotatePasswordScript(newPW))
			if err != nil || strings.Contains(out, "bmV3") {
				t.Fatalf("err=%v, the new password must not be printed:\n%s", err, out)
			}
			y, k := readRel(dir, rel(ExitHysteriaConfig)), readRel(dir, rel(ExitKeysPath))
			if strings.Contains(y+k, "b2xk") || !strings.Contains(y, "\n  password: "+newPW+"\n") || !strings.Contains(y, "\n    password: keep-me\n") ||
				!regexp.MustCompile(`"ssPassword": ?"`+regexp.QuoteMeta(newPW)+`"`).MatchString(k) || !strings.Contains(k, `"uuid"`) {
				t.Fatalf("yaml:\n%s\nkeys:\n%s", y, k)
			}
			for _, b := range []string{ExitHysteriaConfig, ExitKeysPath} {
				if exists(dir, rel(b)+".bak") {
					t.Errorf("%s.bak with the old password left", b)
				}
				if fi, err := os.Stat(filepath.Join(dir, rel(b))); runtime.GOOS != "windows" && (err != nil || fi.Mode().Perm() != 0o600) {
					t.Errorf("%s mode %v %v", b, fi.Mode(), err)
				}
			}
			if left, _ := filepath.Glob(filepath.Join(dir, "opt/heyearth/*/*.??????")); len(left) != 0 {
				t.Errorf("temp files left: %v", left)
			}
			chk, _ := shellRun(t, dir, "", ExitPasswordCheckScript(newPW))
			if chk != "YAML=new\nKEYS=new\n" {
				t.Fatalf("check after rotation: %q", chk)
			}
			chk, _ = shellRun(t, dir, "", ExitPasswordCheckScript(oldPW))
			if chk != "YAML=old\nKEYS=old\n" {
				t.Fatalf("check with another password: %q", chk)
			}
		})
	}
}

// The tunnel password has a second copy wherever a hysteria server config sits at
// ExitHysteriaConfigLegacy (exit 2, 2026-10-26): deprovision rotates it too — a copy left with the
// old password would make a later «Настроить» find two different passwords on the node and
// refuse. The check reports the worse of the two copies; an unknown format in either copy
// changes nothing at all.
func TestExitRotatePasswordRotatesBothHysteriaCopies(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	const oldPW, newPW = "b2xkLXBhc3N3b3JkLXh4eA==", "bmV3LXBhc3N3b3JkLXl5eQ=="
	if ExitHysteriaConfigLegacy != "/etc/hysteria/server.yaml" || ExitHysteriaConfigLegacy == ExitHysteriaConfig {
		t.Fatal(ExitHysteriaConfigLegacy)
	}
	legacyYAML := "listen: :50443\nauth:\n  type: password\n  password: " + oldPW + "\nobfs:\n  salamander:\n    password: keep-me\n"
	for name, withMain := range map[string]bool{"both copies": true, "only the /etc copy": false} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if withMain {
				mustWrite(t, dir, rel(ExitHysteriaConfig), ExitHysteriaServerYAML(oldPW, "203.0.113.7", 8443), 0o600)
			}
			mustWrite(t, dir, rel(ExitHysteriaConfigLegacy), legacyYAML, 0o640)
			mustWrite(t, dir, rel(ExitHysteriaConfigLegacy)+".bak", "  password: "+oldPW+"\n", 0o640)
			mustWrite(t, dir, rel(ExitKeysPath), `{"privateKey": "priv", "publicKey": "pub", "shortId": "ab"}`, 0o600)
			if chk, _ := shellRun(t, dir, "", ExitPasswordCheckScript(newPW)); chk != "YAML=old\nKEYS=none\n" {
				t.Fatalf("check before rotation: %q", chk)
			}
			out, err := shellRun(t, dir, "", ExitRotatePasswordScript(newPW))
			if err != nil || strings.Contains(out, "bmV3") || !strings.Contains(out, ExitHysteriaConfigLegacy) {
				t.Fatalf("err=%v:\n%s", err, out)
			}
			copies := []string{ExitHysteriaConfigLegacy}
			if withMain {
				copies = append(copies, ExitHysteriaConfig)
			}
			for _, p := range copies {
				y := readRel(dir, rel(p))
				if strings.Contains(y, oldPW) || !strings.Contains(y, "\n  password: "+newPW+"\n") || exists(dir, rel(p)+".bak") {
					t.Fatalf("%s not rotated (.bak left %v):\n%s", p, exists(dir, rel(p)+".bak"), y)
				}
			}
			if y := readRel(dir, rel(ExitHysteriaConfigLegacy)); !strings.Contains(y, "\n    password: keep-me\n") {
				t.Fatalf("the nested obfs password must stay:\n%s", y)
			}
			if fi, err := os.Stat(filepath.Join(dir, rel(ExitHysteriaConfigLegacy))); runtime.GOOS != "windows" && (err != nil || fi.Mode().Perm() != 0o640) {
				t.Errorf("mode of the /etc copy not kept: %v %v", fi.Mode(), err)
			}
			if chk, _ := shellRun(t, dir, "", ExitPasswordCheckScript(newPW)); chk != "YAML=new\nKEYS=none\n" {
				t.Fatalf("check after rotation: %q", chk)
			}
			// One copy back on the old password: the check sees it whichever copy it is.
			mustWrite(t, dir, rel(ExitHysteriaConfigLegacy), legacyYAML, 0o640)
			if chk, _ := shellRun(t, dir, "", ExitPasswordCheckScript(newPW)); chk != "YAML=old\nKEYS=none\n" {
				t.Fatalf("an old /etc copy must count: %q", chk)
			}
		})
	}
	// An /etc copy of an unknown format (no "  password:" line): nothing is rotated, not even the
	// main copy or the keys.
	dir := t.TempDir()
	mainYAML := ExitHysteriaServerYAML(oldPW, "203.0.113.7", 8443)
	mustWrite(t, dir, rel(ExitHysteriaConfig), mainYAML, 0o600)
	mustWrite(t, dir, rel(ExitHysteriaConfigLegacy), "auth:\n    type: password\n    password: "+oldPW+"\n", 0o600)
	mustWrite(t, dir, rel(ExitKeysPath), `{"ssPassword": "`+oldPW+`"}`, 0o600)
	if out, err := shellRun(t, dir, "", ExitRotatePasswordScript(newPW)); err == nil || !strings.Contains(out, ExitHysteriaConfigLegacy) {
		t.Fatalf("unknown /etc copy accepted: %v\n%s", err, out)
	}
	if readRel(dir, rel(ExitHysteriaConfig)) != mainYAML || !strings.Contains(readRel(dir, rel(ExitKeysPath)), oldPW) {
		t.Fatal("a refused rotation changed a file")
	}
	if chk, _ := shellRun(t, dir, "", ExitPasswordCheckScript(newPW)); chk != "YAML=old\nKEYS=old\n" {
		t.Fatalf("%q", chk)
	}
}

// The rotated server.yaml line is exactly what the template renders for that password: YAML
// would read a bare null / true / 123 as no string at all (hysteria would get another password).
func TestExitRotatePasswordYAMLScalars(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	lineRe := regexp.MustCompile(`(?m)^  password: .*$`)
	for _, pw := range []string{"null", "true", "123", "bmV3LXBhc3N3b3JkLXl5eQ=="} {
		dir := t.TempDir()
		mustWrite(t, dir, rel(ExitHysteriaConfig), ExitHysteriaServerYAML("b2xkLXBhc3N3b3JkLXh4eA==", "203.0.113.7", 0), 0o600)
		mustWrite(t, dir, rel(ExitKeysPath), `{"ssPassword": "b2xkLXBhc3N3b3JkLXh4eA=="}`, 0o600)
		if out, err := shellRun(t, dir, "", ExitRotatePasswordScript(pw)); err != nil {
			t.Fatalf("%s: %v\n%s", pw, err, out)
		}
		want := lineRe.FindString(ExitHysteriaServerYAML(pw, "203.0.113.7", 0))
		got := lineRe.FindAllString(readRel(dir, rel(ExitHysteriaConfig)), -1)
		if len(got) != 1 || got[0] != want || (pw != "bmV3LXBhc3N3b3JkLXl5eQ==" && want != `  password: "`+pw+`"`) {
			t.Fatalf("%s: yaml line %q, the template renders %q", pw, got, want)
		}
		if k := readRel(dir, rel(ExitKeysPath)); k != `{"ssPassword": "`+pw+`"}`+"\n" {
			t.Fatalf("%s: keys %q", pw, k)
		}
		if out, _ := shellRun(t, dir, "", ExitPasswordCheckScript(pw)); out != "YAML=new\nKEYS=new\n" {
			t.Fatalf("%s: check %q", pw, out)
		}
	}
	// A plain `password: null` line is not the rotated "null".
	dir := t.TempDir()
	mustWrite(t, dir, rel(ExitHysteriaConfig), "auth:\n  type: password\n  password: null\n", 0o600)
	if out, _ := shellRun(t, dir, "", ExitPasswordCheckScript("null")); out != "YAML=old\nKEYS=absent\n" {
		t.Fatalf("bare null accepted: %q", out)
	}
}

// The names deprovision removes are the ones the templates install: a renamed constant or
// table cannot leave a rule set behind unnoticed.
func TestTeardownNamesMatchTemplates(t *testing.T) {
	guard, err := ExitGuardConf("203.0.113.7", 8443)
	if err != nil {
		t.Fatal(err)
	}
	for name, ok := range map[string]bool{
		"hop table":       strings.Contains(ExitPortHopConf, "table inet "+ExitPortHopTable+" {"),
		"hop conf":        strings.Contains(ExitPortHopUnit, "nft -f "+ExitPortHopConfPath+"\n"),
		"hop unit path":   ExitPortHopUnitPath == unitFilePath(ExitPortHopService),
		"hop check":       strings.Contains(HysteriaCheckScript, "table inet "+ExitPortHopTable+" "),
		"guard table":     strings.Contains(guard, "table inet "+ExitGuardTable+" {"),
		"guard conf":      strings.Contains(ExitGuardUnit, "nft -f "+ExitGuardConfPath+"\n"),
		"guard unit path": ExitGuardUnitPath == unitFilePath(ExitGuardService),
		"stls requires":   strings.Contains(ExitSTLSUnitFile, "Requires="+ExitGuardService+".service"),
		"service units": slices.Equal(ExitServiceUnits()[:4], []string{"hysteria2", ExitSTLSUnit, "xray", "node-agent"}) &&
			ExitHysteriaUnitPath == unitFilePath("hysteria2") && ExitSTLSUnitPath == unitFilePath(ExitSTLSUnit) &&
			ExitXrayUnitPath == unitFilePath("xray") && NodeAgentUnitPath == unitFilePath("node-agent"),
		"hub client": strings.Contains(HubHysteriaClientUnit(7, "203.0.113.7"), " -c "+HubClientConfigPath(7)+"\n") &&
			strings.Contains(HubHysteriaClientYAML(7, "203.0.113.7", "x", 1095), "server id=7"),
		"hub stls client": strings.Contains(HubSTLSClientUnit(7, "203.0.113.7"), "LoadCredential=client.json:"+HubSTLSConfigPath(7)+"\n") &&
			strings.Contains(HubClientRemoveScript(7), shQuote(RestartMarker(HubSTLSUnit(7)))) &&
			strings.Contains(HubClientRemoveScript(7), shQuote(PathCheckMarker(HubSTLSUnit(7)))),
		// The Hub client's markers are the ones its converge and rollback use (the path check of a live exit included).
		"hub client markers": strings.Contains(HubClientRemoveScript(7), shQuote(RestartMarker(HubClientUnit(7)))) &&
			strings.Contains(HubClientRemoveScript(7), shQuote(PathCheckMarker(HubClientUnit(7)))) &&
			strings.Contains(UnitConvergeSoftScript(HubClientUnit(7), ""), shQuote(PathCheckMarker(HubClientUnit(7)))) &&
			strings.Contains(UnitRollbackScript(HubClientUnit(7), nil), shQuote(PathCheckMarker(HubClientUnit(7)))),
	} {
		if !ok {
			t.Errorf("%s: teardown and template disagree", name)
		}
	}
}

func TestExitRotatePasswordRefuses(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	const oldPW = "b2xkLXBhc3N3b3JkLXh4eA=="
	// A server.yaml without the auth password line is an unknown format: nothing is changed (the
	// keys file neither), the step fails instead of leaving the old password in place.
	dir := t.TempDir()
	mustWrite(t, dir, rel(ExitHysteriaConfig), "listen: :50443\nauth:\n  type: userpass\n", 0o600)
	mustWrite(t, dir, rel(ExitKeysPath), `{"ssPassword": "`+oldPW+`"}`, 0o600)
	if out, err := shellRun(t, dir, "", ExitRotatePasswordScript("bmV3LXBhc3N3b3JkLXl5eQ==")); err == nil ||
		!strings.Contains(readRel(dir, rel(ExitKeysPath)), oldPW) {
		t.Fatalf("unknown yaml accepted: %v\n%s", err, out)
	}
	// Nothing installed: nothing to rotate.
	dir = t.TempDir()
	if out, err := shellRun(t, dir, "", ExitRotatePasswordScript("bmV3LXBhc3N3b3JkLXl5eQ==")); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if out, _ := shellRun(t, dir, "", ExitPasswordCheckScript("bmV3")); out != "YAML=absent\nKEYS=absent\n" {
		t.Fatalf("%q", out)
	}
	mustWrite(t, dir, rel(ExitKeysPath), `{"uuid": "u"}`, 0o600)
	if out, _ := shellRun(t, dir, "", ExitPasswordCheckScript("bmV3")); out != "YAML=absent\nKEYS=none\n" {
		t.Fatalf("%q", out)
	}
	// Only base64 reaches the files (YAML plain scalar, JSON string): anything else is refused,
	// and quoting keeps it a literal.
	mustWrite(t, dir, rel(ExitHysteriaConfig), ExitHysteriaServerYAML(oldPW, "203.0.113.7", 0), 0o600)
	for _, bad := range []string{"", "x y", `a"b`, "x'; touch ./pwned; '", "a\nb"} {
		if out, err := shellRun(t, dir, "", ExitRotatePasswordScript(bad)); err == nil {
			t.Errorf("password %q accepted:\n%s", bad, out)
		}
	}
	if exists(dir, "pwned") || !strings.Contains(readRel(dir, rel(ExitHysteriaConfig)), oldPW) {
		t.Fatal("a refused password changed something")
	}
}

func TestHubClientRemoveArchivesAndCleans(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	seed := func(id string) {
		mustWrite(t, dir, "opt/heyearth/hysteria/client-"+id+".yaml", "auth: x", 0o600)
		mustWrite(t, dir, "opt/heyearth/hysteria/client-"+id+".yaml.bak", "auth: old", 0o600)
		unit(t, dir, "hysteria2-client-"+id, true, true)
		mustWrite(t, dir, "etc/systemd/system/hysteria2-client-"+id+".service.bak", "[Unit]", 0o644)
		mustWrite(t, dir, "etc/systemd/system/multi-user.target.wants/hysteria2-client-"+id+".service", "", 0o644)
		mustWrite(t, dir, rel(RestartMarker("hysteria2-client-"+id)), "", 0o644)
		mustWrite(t, dir, rel(PathCheckMarker("hysteria2-client-"+id)), "BEFORE=1\n", 0o644)
		// The ShadowTLS backup client of the same exit.
		s := "heyearth-stls-client-" + id
		mustWrite(t, dir, "opt/heyearth/stls/client-"+id+".json", `{"password":"x"}`, 0o600)
		mustWrite(t, dir, "opt/heyearth/stls/client-"+id+".json.bak", `{"password":"y"}`, 0o600)
		unit(t, dir, s, true, true)
		mustWrite(t, dir, "etc/systemd/system/"+s+".service.bak", "[Unit]", 0o644)
		mustWrite(t, dir, "etc/systemd/system/multi-user.target.wants/"+s+".service", "", 0o644)
		mustWrite(t, dir, rel(RestartMarker(s)), "", 0o644)
		mustWrite(t, dir, rel(BinaryPendingMarker(s)), "x", 0o644)
		mustWrite(t, dir, rel(PathCheckMarker(s)), "BEFORE=1\n", 0o644)
	}
	seed("7")
	seed("17") // another exit: untouched
	if out, _ := shellRun(t, dir, teardownStub, HubClientStateScript(7)); out != "UNIT=1\nCONFIG=1\nACTIVE=1\nLINK=1\n" {
		t.Fatalf("state before: %q", out)
	}
	out, err := shellRun(t, dir, teardownStub, HubClientRemoveScript(7))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, gone := range []string{"opt/heyearth/hysteria/client-7.yaml", "opt/heyearth/hysteria/client-7.yaml.bak",
		"etc/systemd/system/hysteria2-client-7.service", "etc/systemd/system/hysteria2-client-7.service.bak",
		"etc/systemd/system/multi-user.target.wants/hysteria2-client-7.service", "active.hysteria2-client-7", "enabled.hysteria2-client-7",
		rel(RestartMarker("hysteria2-client-7")), rel(PathCheckMarker("hysteria2-client-7")),
		"opt/heyearth/stls/client-7.json", "opt/heyearth/stls/client-7.json.bak", "etc/systemd/system/heyearth-stls-client-7.service",
		"etc/systemd/system/heyearth-stls-client-7.service.bak", "etc/systemd/system/multi-user.target.wants/heyearth-stls-client-7.service",
		"active.heyearth-stls-client-7", "enabled.heyearth-stls-client-7", rel(RestartMarker("heyearth-stls-client-7")),
		rel(BinaryPendingMarker("heyearth-stls-client-7")), rel(PathCheckMarker("heyearth-stls-client-7"))} {
		if exists(dir, gone) {
			t.Errorf("%s left behind", gone)
		}
	}
	for _, kept := range []string{"opt/heyearth/hysteria/client-17.yaml", "etc/systemd/system/hysteria2-client-17.service",
		"active.hysteria2-client-17", "enabled.hysteria2-client-17", rel(PathCheckMarker("hysteria2-client-17")),
		"opt/heyearth/stls/client-17.json", "active.heyearth-stls-client-17", rel(PathCheckMarker("heyearth-stls-client-17"))} {
		if !exists(dir, kept) {
			t.Errorf("another exit's %s removed", kept)
		}
	}
	arch, _ := filepath.Glob(filepath.Join(dir, "opt/heyearth/state/server-archive/7-*"))
	if len(arch) != 1 {
		t.Fatalf("archives %v\n%s", arch, out)
	}
	a := filepath.ToSlash(mustRel(t, dir, arch[0]))
	for _, f := range []string{"client-7.yaml", "hysteria2-client-7.service", "hysteria2-client-7.journal.log",
		"client-7.json", "heyearth-stls-client-7.service", "heyearth-stls-client-7.journal.log"} {
		if !exists(dir, a+"/"+f) {
			t.Fatalf("%s not archived in %s\n%s", f, a, out)
		}
	}
	if fi, err := os.Stat(arch[0]); runtime.GOOS != "windows" && (err != nil || fi.Mode().Perm()&0o077 != 0) {
		t.Errorf("the archive holds the old tunnel password: mode %v", fi.Mode())
	}
	// The stray-process fallbacks match exit 7's clients only.
	pats := strings.Split(strings.TrimSpace(readRel(dir, "pkill.log")), "\n")
	if len(pats) != 2 {
		t.Fatalf("pkill calls %q", pats)
	}
	for i, c := range []struct{ mine, other, longer string }{
		{"/usr/local/bin/hysteria client -c /opt/heyearth/hysteria/client-7.yaml", "/usr/local/bin/hysteria client -c /opt/heyearth/hysteria/client-17.yaml",
			"/usr/local/bin/hysteria client -c /opt/heyearth/hysteria/client-70.yaml"},
		{SingBoxBinary + " run -c /run/credentials/heyearth-stls-client-7.service/client.json",
			SingBoxBinary + " run -c /run/credentials/heyearth-stls-client-17.service/client.json",
			SingBoxBinary + " run -c /run/credentials/heyearth-stls-client-70.service/client.json"},
	} {
		pat := strings.Trim(pats[i], "'")
		re := regexp.MustCompile(strings.Trim(strings.TrimPrefix(pat, "-9 -f "), "'"))
		if !strings.HasPrefix(pat, "-9 -f ") || !re.MatchString(c.mine) || re.MatchString(c.other) || re.MatchString(c.longer) {
			t.Fatalf("pkill pattern %q", pat)
		}
	}
	if out, _ := shellRun(t, dir, teardownStub, HubClientStateScript(7)); out != "UNIT=0\nCONFIG=0\nACTIVE=0\nLINK=0\n" {
		t.Fatalf("state after: %q", out)
	}
	// Idempotent: a second run archives nothing new and still succeeds.
	if out, err := shellRun(t, dir, teardownStub, HubClientRemoveScript(7)); err != nil {
		t.Fatalf("second run: %v\n%s", err, out)
	}
	if arch, _ := filepath.Glob(filepath.Join(dir, "opt/heyearth/state/server-archive/*")); len(arch) != 1 {
		t.Fatalf("archives %v", arch)
	}
	// A client that would not stop fails the step.
	seed("8")
	if out, err := shellRun(t, dir, teardownStub, HubClientRemoveScript(8), "STUCK=hysteria2-client-8"); err == nil {
		t.Fatalf("a running client accepted:\n%s", out)
	}
}

func mustRel(t *testing.T, base, p string) string {
	t.Helper()
	r, err := filepath.Rel(base, p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The exit's nft rule sets: the xray egress guard and the port hop go, the node-agent Hub-only
// guard stays (a parked node keeps :50052 closed to the world), nftables.service is never touched.
func TestExitRemoveGuardAndHop(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	for _, f := range []string{ExitGuardConfPath, ExitPortHopConfPath, ExitAgentGuardConfPath} {
		mustWrite(t, dir, rel(f), "table inet x {}\n", 0o644)
		mustWrite(t, dir, rel(f)+".bak", "table inet x {}\n", 0o644)
	}
	for _, u := range []string{ExitGuardService, ExitPortHopService, ExitAgentGuardService} {
		unit(t, dir, u, true, true)
		mustWrite(t, dir, "etc/systemd/system/multi-user.target.wants/"+u+".service", "", 0o644)
	}
	for _, tb := range []string{ExitGuardTable, ExitPortHopTable, ExitAgentGuardTable} {
		mustWrite(t, dir, "nft."+tb, "table inet "+tb+" {}\n", 0o644)
	}
	unit(t, dir, "xray", true, true)
	unit(t, dir, "nftables", false, false)
	state := func() map[string]string {
		out, err := shellRun(t, dir, teardownStub, ExitTeardownStateScript)
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		kv := map[string]string{}
		for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
			k, v, _ := strings.Cut(l, "=")
			kv[k] = v
		}
		return kv
	}
	if s := state(); s["GUARD_TABLE"] != "1" || s["GUARD_FILES"] != "1" || s["HOP_TABLE"] != "1" || s["HOP_FILES"] != "1" ||
		s["ACTIVE_xray"] != "1" || s["ENABLED_xray"] != "1" || s["ACTIVE_mtproxy"] != "0" || s["ENABLED_"+ExitPortHopService] != "1" {
		t.Fatalf("state before: %v", s)
	}
	// xray still running: its guard is not taken away.
	if out, err := shellRun(t, dir, teardownStub, ExitRemoveGuardScript); err == nil || !exists(dir, "nft."+ExitGuardTable) || !exists(dir, rel(ExitGuardConfPath)) {
		t.Fatalf("guard removed under a running xray: %v\n%s", err, out)
	}
	os.Remove(filepath.Join(dir, "active.xray"))
	for run := 0; run < 2; run++ {
		for _, s := range []string{ExitRemoveGuardScript, ExitRemoveHopScript} {
			if out, err := shellRun(t, dir, teardownStub, s); err != nil {
				t.Fatalf("run %d: %v\n%s", run, err, out)
			}
		}
	}
	for _, gone := range []string{"nft." + ExitGuardTable, "nft." + ExitPortHopTable, rel(ExitGuardConfPath), rel(ExitGuardConfPath) + ".bak",
		rel(ExitGuardUnitPath), rel(ExitPortHopConfPath), rel(ExitPortHopConfPath) + ".bak", rel(ExitPortHopUnitPath),
		"etc/systemd/system/multi-user.target.wants/" + ExitGuardService + ".service", "etc/systemd/system/multi-user.target.wants/" + ExitPortHopService + ".service",
		"active." + ExitGuardService, "enabled." + ExitPortHopService} {
		if exists(dir, gone) {
			t.Errorf("%s left behind", gone)
		}
	}
	for _, kept := range []string{"nft." + ExitAgentGuardTable, rel(ExitAgentGuardConfPath), "etc/systemd/system/" + ExitAgentGuardService + ".service",
		"active." + ExitAgentGuardService, "enabled." + ExitAgentGuardService} {
		if !exists(dir, kept) {
			t.Errorf("the node-agent guard's %s removed", kept)
		}
	}
	if c := readRel(dir, "calls") + readRel(dir, "nft.log"); strings.Contains(c, "agent") || regexp.MustCompile(`(?m) nftables$`).MatchString(c) {
		t.Fatalf("touched the agent guard or nftables.service:\n%s", c)
	}
	if s := state(); s["GUARD_TABLE"] != "0" || s["GUARD_FILES"] != "0" || s["HOP_TABLE"] != "0" || s["HOP_FILES"] != "0" ||
		s["ACTIVE_"+ExitGuardService] != "0" || s["ENABLED_"+ExitPortHopService] != "0" {
		t.Fatalf("state after: %v", s)
	}
	// A table nft refuses to delete fails the step.
	mustWrite(t, dir, "nft."+ExitPortHopTable, "table inet "+ExitPortHopTable+" {}\n", 0o644)
	if out, err := shellRun(t, dir, teardownStub, ExitRemoveHopScript, "NFT_STUCK=1"); err == nil {
		t.Fatalf("a table left behind accepted:\n%s", out)
	}
}

func TestMTProxyRunScript(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	const secret = "ee00112233445566778899aabbccddeeff6578616d706c652e6f7267"
	dir := t.TempDir()
	// The installer is sourced in a subshell: it gets the arguments as $1..$3, its exit ends only
	// the subshell (and fails the script).
	mustWrite(t, dir, rel(MTProxyInstallerPath), "set -euo pipefail\nprintf '%s\\n' \"$@\" > ./mt.args\n"+
		"if [ \"${MT_FAIL:-0}\" = 1 ]; then echo 'ERR: fronting domain' >&2; exit 1; fi\necho \"secret: $2\"\n", 0o700)
	run := MTProxyRunScript(MTProxyInstallerPath, 8443, secret, "example.org")
	out, err := shellRun(t, dir, "", run)
	if err != nil || readRel(dir, "mt.args") != "8443\n"+secret+"\nexample.org\n" || !strings.Contains(out, "secret: "+secret) {
		t.Fatalf("%v %q\n%s", err, readRel(dir, "mt.args"), out)
	}
	if out, err := shellRun(t, dir, "", run+"echo AFTER\n", "MT_FAIL=1"); err == nil || strings.Contains(out, "AFTER") {
		t.Fatalf("a failing installer must fail the script: %v\n%s", err, out)
	}
	if _, err := shellRun(t, dir, "", MTProxyRunScript(MTProxyInstallerPath, 8443, secret, "x'; touch ./pwned; '")); err != nil || exists(dir, "pwned") ||
		!strings.Contains(readRel(dir, "mt.args"), "x'; touch ./pwned; '") {
		t.Fatalf("argument escaped: %v %q", err, readRel(dir, "mt.args"))
	}
}

func TestMTProxyCheckScript(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	const secret = "ee00112233445566778899aabbccddeeff6578616d706c652e6f7267"
	check := strings.NewReplacer("/usr/local/bin/", "./bin/", "/opt/mtproxy/", "./opt/mtproxy/").Replace(MTProxyCheckScript(8443, secret))
	stubs := systemctlStub + "ss() { echo \"$*\" >> ./ss.args; cat ./ss.out 2>/dev/null; }\n"
	dir := t.TempDir()
	mustWrite(t, dir, "bin/mtg", "#!/bin/sh\necho '2.2.8 (go1.22.5, linux/amd64)'\n", 0o755)
	mustWrite(t, dir, "opt/mtproxy/config.toml", "secret = \""+secret+"\"\nbind-to = \"0.0.0.0:8443\"\n", 0o600)
	mustWrite(t, dir, "ss.out", `LISTEN 0 4096 *:8443 *:* users:(("mtg",pid=812,fd=3))`+"\n", 0o644)
	mustWrite(t, dir, "active.mtproxy", "", 0o644)
	if out, _ := shellRun(t, dir, stubs, check); out != "ACTIVE=1\nLISTEN=1\nSECRET=1\nVERSION=2.2.8\n" || !strings.Contains(readRel(dir, "ss.args"), "sport = :8443") {
		t.Fatalf("%q %q", out, readRel(dir, "ss.args"))
	}
	mustWrite(t, dir, "ss.out", `LISTEN 0 4096 *:8443 *:* users:(("nginx",pid=9,fd=3))`+"\n", 0o644)
	mustWrite(t, dir, "opt/mtproxy/config.toml", "secret = \""+secret+"00\"\n", 0o600) // the wanted secret is only a prefix
	os.Remove(filepath.Join(dir, "active.mtproxy"))
	if out, _ := shellRun(t, dir, stubs, check); out != "ACTIVE=0\nLISTEN=0\nSECRET=0\nVERSION=2.2.8\n" {
		t.Fatalf("%q", out)
	}
	if strings.Contains(MTProxyCheckScript(8443, secret), "grep -qF -- \"$S\"") {
		t.Fatal("the secret must not be passed to grep's argv")
	}
}

// Every teardown/MTProxy op script starts with its exact marker line and parses under bash -n.
func TestTeardownScriptsMarked(t *testing.T) {
	for op, s := range map[string]string{
		"hub-client-remove 7": HubClientRemoveScript(7), "hub-client-state 7": HubClientStateScript(7),
		"rotate-password": ExitRotatePasswordScript("eA=="), "password-check": ExitPasswordCheckScript("eA=="),
		"stop-services": ExitStopServicesScript, "remove-guard": ExitRemoveGuardScript, "remove-hop": ExitRemoveHopScript,
		"teardown-state": ExitTeardownStateScript, "mtproxy-install": MTProxyRunScript(MTProxyInstallerPath, 8443, "ee00", "example.org"),
		"mtproxy-check": MTProxyCheckScript(8443, "ee00"),
	} {
		if first, _, _ := strings.Cut(s, "\n"); first != "# hp:"+op {
			t.Errorf("marker %q, want %q", first, "# hp:"+op)
		}
		if haveBash() {
			if out, err := exec.Command("bash", "-n", "-c", s).CombinedOutput(); err != nil {
				t.Errorf("%s: bash -n: %v %s", op, err, out)
			}
		}
	}
}
