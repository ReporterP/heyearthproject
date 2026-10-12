package main

import (
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/config"
)

func TestSetupXrayScript(t *testing.T) {
	for _, force := range []bool{false, true} {
		s := setupXrayScript(force)
		want := "FORCE=0\n"
		if force {
			want = "FORCE=1\n"
		}
		for _, w := range []string{want, xrayBootstrapBlock,
			// a live Hub is not restarted for nothing
			`if [ "$XRAY_BOOTSTRAP_WROTE" = 1 ] || ! systemctl is-active --quiet xray; then`} {
			if !strings.Contains(s, w) {
				t.Errorf("force=%v: script lacks %q", force, w)
			}
		}
		if strings.Contains(s, "apple.com") {
			t.Errorf("force=%v: apple.com is a bad SNI for the RU Hub", force)
		}
		if n := strings.Count(s, "systemctl restart xray"); n != 1 {
			t.Errorf("force=%v: %d xray restarts, want exactly the guarded one", force, n)
		}
		if haveBash() {
			if out, code := runBashStdin(t, s, "-n"); code != 0 {
				t.Errorf("force=%v: bash -n failed (%d): %s", force, code, out)
			}
		}
	}
	sni := config.DefaultRealitySNI
	for _, w := range []string{`"dest": "` + sni + `:443"`, `"serverNames": ["` + sni + `", "www.` + sni + `"]`} {
		if !strings.Contains(xrayBootstrapBlock, w) {
			t.Errorf("bootstrap lacks %q", w)
		}
	}
}

// The bootstrap never replaces an existing config.json unless forced, writes atomically, and
// leaves the old file alone when xray rejects the new one.
func TestXrayBootstrapBlock(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	run := func(pre string) string {
		t.Helper()
		out, exit := runBash(t, `T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
export XRAY_CONF="$T/config.json" XRAY_BIN="$T/xray"
UUID=11111111-2222-3333-4444-555555555555; PRIV_KEY=priv; SHORT_ID=abcd; FORCE=0
`+pre+`
cat > "$T/block.sh" <<'BLOCK_EOF'
`+xrayBootstrapBlock+`
BLOCK_EOF
( set -e; . "$T/block.sh"; echo "STATE wrote=$XRAY_BOOTSTRAP_WROTE" ); echo "STATE rc=$?"
echo "STATE conf=$(tr -d '\n ' < "$T/config.json" 2>/dev/null | head -c 2000)"
echo "STATE tmp=$(ls "$T" | grep -c tmp)"
`)
		if exit != 0 {
			t.Fatalf("harness exit %d:\n%s", exit, out)
		}
		return out
	}
	want := func(t *testing.T, out string, lines ...string) {
		t.Helper()
		for _, l := range lines {
			if !hasLine(out, l) {
				t.Errorf("missing %q:\n%s", l, out)
			}
		}
	}
	gen := `"id":"11111111-2222-3333-4444-555555555555"`
	t.Run("a live Hub's generated config is kept", func(t *testing.T) {
		out := run(`echo '{"generated":true}' > "$XRAY_CONF"`)
		want(t, out, "STATE wrote=0", "STATE rc=0", `STATE conf={"generated":true}`, "STATE tmp=0")
		if !strings.Contains(out, "bootstrap НЕ записываю") {
			t.Errorf("no notice:\n%s", out)
		}
	})
	t.Run("--force replaces it", func(t *testing.T) {
		out := run(`echo '{"generated":true}' > "$XRAY_CONF"; FORCE=1`)
		want(t, out, "STATE wrote=1", "STATE rc=0", "STATE tmp=0")
		if c := lineWithPrefix(out, "STATE conf="); !strings.Contains(c, gen) || !strings.Contains(c, `"dest":"`+config.DefaultRealitySNI+`:443"`) {
			t.Errorf("forced bootstrap not written: %s", c)
		}
	})
	t.Run("a fresh Hub gets the bootstrap", func(t *testing.T) {
		out := run(``)
		want(t, out, "STATE wrote=1", "STATE rc=0", "STATE tmp=0")
		if c := lineWithPrefix(out, "STATE conf="); !strings.Contains(c, gen) || !strings.Contains(c, `"privateKey":"priv"`) {
			t.Errorf("bootstrap not written: %s", c)
		}
	})
	t.Run("xray rejects the bootstrap: nothing replaced, error", func(t *testing.T) {
		out := run(`echo '{"generated":true}' > "$XRAY_CONF"; FORCE=1; printf '#!/bin/sh\nexit 1\n' > "$XRAY_BIN"; chmod +x "$XRAY_BIN"`)
		want(t, out, "STATE rc=1", `STATE conf={"generated":true}`, "STATE tmp=0")
	})
}

// The seed never overwrites the SNI the auto-tuner / admin panel chose (a --setup-xray after a
// DB restore would otherwise put the bootstrap SNI back).
func TestSeedConnectionSettingsScript(t *testing.T) {
	s := seedConnectionSettingsScript("203.0.113.5")
	if !strings.Contains(s, `MAIN_HOST="203.0.113.5"`) {
		t.Error("host not rendered")
	}
	i := strings.Index(s, "('reality_sni_domain'")
	if i < 0 {
		t.Fatal("no reality_sni_domain seed")
	}
	line := s[i : i+strings.Index(s[i:], "\n")]
	if !strings.Contains(line, "ON CONFLICT (key) DO NOTHING") {
		t.Errorf("reality_sni_domain must only be inserted when absent: %s", line)
	}
	if strings.Contains(s, "postgres://") || strings.Contains(s, "%!") {
		t.Error("the seed must use peer auth, not a DB URL")
	}
	if haveBash() {
		if out, code := runBashStdin(t, s, "-n"); code != 0 {
			t.Errorf("bash -n failed (%d): %s", code, out)
		}
	}
}
