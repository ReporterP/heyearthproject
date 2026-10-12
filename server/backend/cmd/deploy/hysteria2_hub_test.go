package main

import (
	"regexp"
	"strings"
	"testing"
)

// The port-hop rule must never install REDIRECT (it rewrites the destination to the
// interface's primary IP and the server's replies then clash in conntrack, see
// hysteria2PortHopRule). Removing a stale REDIRECT is fine; adding one is the bug.
func TestHysteria2HubScriptNeverAddsRedirect(t *testing.T) {
	script := hysteria2HubScript()
	addRedirect := regexp.MustCompile(`iptables -t nat -[AI] PREROUTING[^\n;|&]*-j REDIRECT`)
	if m := addRedirect.FindString(script); m != "" {
		t.Fatalf("script installs a REDIRECT rule: %q", m)
	}
	if strings.Contains(script, "__PORTHOP__") || strings.Contains(script, "__VERSION__") {
		t.Fatal("placeholder left unreplaced")
	}
	// Applied live and in the unit's ExecStartPre (re-applied on every start, incl. boot).
	if n := strings.Count(script, hysteria2PortHopRule); n != 2 {
		t.Fatalf("port-hop rule appears %d times, want 2 (live + ExecStartPre)", n)
	}
	if !strings.Contains(script, "ExecStartPre=+/bin/sh -c '"+hysteria2PortHopRule+"'") {
		t.Fatal("ExecStartPre does not carry the port-hop rule")
	}
}

func TestHysteria2PortHopRuleShape(t *testing.T) {
	r := hysteria2PortHopRule
	if strings.Contains(r, "'") {
		t.Fatal("rule contains a single quote; it is embedded in sh -c '...'")
	}
	// Port-only DNAT keeps the client's destination IP, so the route-sourced reply matches
	// the conntrack reply tuple.
	for _, want := range []string{
		"-C PREROUTING -p udp --dport 30000:65000 -m addrtype --dst-type LOCAL -j DNAT --to-destination :50443",
		"-I PREROUTING 1 -p udp --dport 30000:65000 -m addrtype --dst-type LOCAL -j DNAT --to-destination :50443",
		"while iptables -t nat -D PREROUTING -p udp --dport 30000:65000 -j REDIRECT --to-ports 50443 2>/dev/null; do :; done",
	} {
		if !strings.Contains(r, want) {
			t.Errorf("rule lacks %q", want)
		}
	}
	if regexp.MustCompile(`--to-destination \d`).MatchString(r) {
		t.Error("DNAT must not pin an IP (port-only)")
	}
}

// PREROUTING also carries forwarded traffic (AWG users on awg0, docker). Every DNAT the
// rule adds must be restricted to packets addressed to the Hub itself, otherwise their
// UDP flows to 30000-65000 get the dport rewritten to 50443 (Discord voice, calls, games).
func TestHysteria2PortHopRuleOnlyLocalDestinations(t *testing.T) {
	r := hysteria2PortHopRule
	adds := regexp.MustCompile(`iptables -t nat -(?:[AI]|C) PREROUTING[^;|&]*`).FindAllString(r, -1)
	if len(adds) != 3 { // -C || -I, then the final -C check
		t.Fatalf("want -C, -I and the final -C check of PREROUTING, got %d: %q", len(adds), adds)
	}
	for _, a := range adds {
		if !strings.Contains(a, "-m addrtype --dst-type LOCAL") {
			t.Errorf("DNAT not restricted to LOCAL destinations: %q", a)
		}
	}
	// The restricted rule goes in FIRST (no moment without a DNAT), then the stale unrestricted
	// DNAT and the legacy REDIRECT are removed in a loop.
	ins := strings.Index(r, "-I PREROUTING 1 -p udp --dport 30000:65000 -m addrtype --dst-type LOCAL")
	del := strings.Index(r, "while iptables -t nat -D PREROUTING -p udp --dport 30000:65000 -j DNAT --to-destination :50443 2>/dev/null; do :; done; ")
	red := strings.Index(r, "while iptables -t nat -D PREROUTING -p udp --dport 30000:65000 -j REDIRECT --to-ports 50443 2>/dev/null; do :; done; ")
	if ins < 0 || del < 0 || red < 0 || ins > del || ins > red {
		t.Errorf("the restricted rule must be inserted before the stale rules are deleted (insert %d, delete DNAT %d, delete REDIRECT %d)", ins, del, red)
	}
	// The last command verifies the restricted rule: ExecStartPre fails loudly without it.
	if !strings.HasSuffix(r, "; "+hysteria2PortHopCheck) {
		t.Error("the restricted rule's -C check must be the last command")
	}
}

// fakeGeoip writes a file that passes or fails geoip_ok: "good" (0x0a first, PRIVATE and RU,
// > 1 MiB), "small", "html" (an error page of the right size), "nomagic".
const fakeGeoip = `fake_geoip() {
  case "$1" in
    good)    { printf '\n'; printf 'PRIVATE RU'; head -c 1100000 /dev/zero; printf '%s' "${2:-}"; } > "$3" ;;
    small)   { printf '\n'; printf 'PRIVATE RU'; } > "$3" ;;
    html)    { printf '<html>PRIVATE RU'; head -c 1100000 /dev/zero; } > "$3" ;;
    nomagic) { printf 'x'; printf 'PRIVATE RU'; head -c 1100000 /dev/zero; } > "$3" ;;
  esac
}
`

func TestHysteriaGeoipCheck(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	out, exit := runBash(t, `T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
`+fakeGeoip+hysteriaGeoipCheck+`for k in good small html nomagic; do fake_geoip "$k" "" "$T/$k"; geoip_ok "$T/$k" && echo "STATE $k=ok" || echo "STATE $k=bad"; done
geoip_ok "$T/missing" && echo "STATE missing=ok" || echo "STATE missing=bad"
`)
	if exit != 0 {
		t.Fatalf("exit %d:\n%s", exit, out)
	}
	for _, l := range []string{"STATE good=ok", "STATE small=bad", "STATE html=bad", "STATE nomagic=bad", "STATE missing=bad"} {
		if !hasLine(out, l) {
			t.Errorf("missing %q:\n%s", l, out)
		}
	}
}

// The monthly cron keeps the current geoip.dat on a failed or implausible download and restarts
// hysteria2-server only when the file actually changed.
func TestHysteriaGeoipCron(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	run := func(mode, existing string) string {
		t.Helper()
		out, exit := runBash(t, `T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
export GEOIP_DST="$T/geoip.dat"
`+fakeGeoip+`MODE='`+mode+`'
if [ -n '`+existing+`' ]; then fake_geoip good '`+existing+`' "$GEOIP_DST"; fi
before=$(cksum < "$GEOIP_DST" 2>/dev/null || echo none)
curl() { while [ $# -gt 0 ]; do if [ "$1" = -o ]; then o="$2"; fi; shift; done
  case "$MODE" in fail) return 22 ;; same) fake_geoip good old "$o" ;; new) fake_geoip good new "$o" ;; *) fake_geoip "$MODE" "" "$o" ;; esac; }
systemctl() { echo "systemctl $*" >> "$T/log"; }
chown() { :; }
command() { if [ "$1" = -v ] && [ "$2" = setfacl ]; then return 1; fi; builtin command "$@"; }
export -f curl systemctl chown command fake_geoip; export MODE T
cat > "$T/cron" <<'CRON_EOF'
`+hysteriaGeoipCron+`CRON_EOF
bash "$T/cron"; echo "STATE rc=$?"
after=$(cksum < "$GEOIP_DST" 2>/dev/null || echo none)
echo "STATE changed=$([ "$before" = "$after" ] && echo 0 || echo 1)"
echo "STATE restarts=$(grep -c 'restart hysteria2-server' "$T/log" 2>/dev/null || echo 0)"
echo "STATE leftover=$(ls "$T" | grep -c '\.new$')"
`)
		if exit != 0 {
			t.Fatalf("harness exit %d:\n%s", exit, out)
		}
		return out
	}
	for _, c := range []struct {
		name, mode, existing string
		want                 []string
	}{
		{"new content: replaced, one restart", "new", "old", []string{"STATE rc=0", "STATE changed=1", "STATE restarts=1", "STATE leftover=0"}},
		{"first download: written, one restart", "new", "", []string{"STATE rc=0", "STATE changed=1", "STATE restarts=1"}},
		{"same content: no restart", "same", "old", []string{"STATE rc=0", "STATE changed=0", "STATE restarts=0", "STATE leftover=0"}},
		{"download fails: old file kept, no restart", "fail", "old", []string{"STATE changed=0", "STATE restarts=0", "STATE leftover=0"}},
		{"HTML instead of geoip.dat: old file kept, no restart, non-zero", "html", "old", []string{"STATE rc=1", "STATE changed=0", "STATE restarts=0", "STATE leftover=0"}},
		{"truncated download: old file kept", "small", "old", []string{"STATE rc=1", "STATE changed=0", "STATE restarts=0"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			out := run(c.mode, c.existing)
			for _, l := range c.want {
				if !hasLine(out, l) {
					t.Errorf("missing %q:\n%s", l, out)
				}
			}
			if c.mode == "fail" && hasLine(out, "STATE rc=0") {
				t.Errorf("a failed download must exit non-zero:\n%s", out)
			}
		})
	}
	// the Hub script installs exactly this cron, and its own download keeps an existing file
	s := hysteria2HubScript()
	if !strings.Contains(s, "cat > /etc/cron.monthly/heyearth-hysteria-geoip <<'CRON'\n"+hysteriaGeoipCron+"CRON\n") {
		t.Error("the Hub script does not install hysteriaGeoipCron verbatim")
	}
	if !strings.Contains(s, "&& geoip_ok /etc/hysteria/geoip.dat.new; then") || !strings.Contains(s, "keeping the current file") {
		t.Error("the setup download must be validated and keep the current file on failure")
	}
	if strings.Contains(s, "__GEOIP") {
		t.Error("geoip placeholder left unreplaced")
	}
}
