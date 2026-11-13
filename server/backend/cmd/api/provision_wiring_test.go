package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/provision"
)

// A mtg secret as biz.BuildMTProtoSecret makes it, fronting example.com.
const testMTSecret = "ee0123456789abcdef0123456789abcdef6578616d706c652e636f6d"

func withExitWiring(t *testing.T, ips []string, settings map[string]string, script string) {
	t.Helper()
	oldIPs, oldSet, oldPath, oldDomain := exitHubIPs, exitSetting, mtproxyScriptPath, exitMTProtoDomainStep
	exitMTProtoDomainStep = func(context.Context, provision.Node, *provision.MTProtoOptions) error { return nil }
	exitHubIPs = func() []string { return ips }
	exitSetting = func(k, def string) string {
		if v, ok := settings[k]; ok {
			return v
		}
		return def
	}
	mtproxyScriptPath = filepath.Join(t.TempDir(), "install-mtproxy.sh")
	if script != "" {
		if err := os.WriteFile(mtproxyScriptPath, []byte(script), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		exitHubIPs, exitSetting, mtproxyScriptPath, exitMTProtoDomainStep = oldIPs, oldSet, oldPath, oldDomain
	})
}

var mtSettings = map[string]string{"mtproto_port": "9443", "mtproto_secret": testMTSecret, "mtproto_fronting_domain": "example.com"}

func TestApiExitOptions(t *testing.T) {
	hub := []string{"198.51.100.1", "2001:db8::1"}
	withExitWiring(t, hub, mtSettings, "#!/bin/bash\nMTG_VERSION=\"2.2.8\"\n")
	o, err := apiExitOptions(provision.Node{ID: 3, MTProtoStatus: "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(o.HubIPs, hub) || o.HubXray != xrayUnitBinary || o.NodeAgent == nil || o.CA == nil {
		t.Fatalf("options %+v", o)
	}
	if o.MTProto == nil || o.MTProtoPort != 9443 || o.MTProto.Port != o.MTProtoPort ||
		o.MTProto.Secret != testMTSecret || o.MTProto.Domain != "example.com" || !strings.Contains(string(o.MTProto.Script), "MTG_VERSION") {
		t.Fatalf("MTProxy options %+v / %d", o.MTProto, o.MTProtoPort)
	}
	// MTProxy switched off in the wizard: no MTProxy step, the port still in the ACL / guard rules.
	o, err = apiExitOptions(provision.Node{ID: 3, MTProtoStatus: "disabled"})
	if err != nil || o.MTProto != nil || o.MTProtoPort != 9443 {
		t.Fatalf("disabled: %+v %v", o, err)
	}
}

// «Переустановить Telegram-прокси» does not depend on the Hub address list.
func TestApiMTProtoOptionsWithoutHubIPs(t *testing.T) {
	withExitWiring(t, nil, mtSettings, "x")
	mt, err := apiMTProtoOptions(provision.Node{ID: 3, MTProtoStatus: "unhealthy"})
	if err != nil || mt == nil || mt.Port != 9443 {
		t.Fatalf("%+v %v", mt, err)
	}
	if mt, err := apiMTProtoOptions(provision.Node{ID: 3, MTProtoStatus: "disabled"}); mt != nil || err != nil {
		t.Fatalf("disabled: %+v %v", mt, err)
	}
}

func TestApiExitOptionsRefusals(t *testing.T) {
	withExitWiring(t, nil, mtSettings, "x")
	if _, err := apiExitOptions(provision.Node{ID: 3, MTProtoStatus: "disabled"}); err == nil || !strings.Contains(err.Error(), "адреса Hub") {
		t.Fatalf("no Hub address must refuse (the agent guard would lock the Hub out): %v", err)
	}
	withExitWiring(t, []string{"198.51.100.1"}, mtSettings, "")
	if _, err := apiExitOptions(provision.Node{ID: 3, MTProtoStatus: "unknown"}); err == nil || !strings.Contains(err.Error(), "--ensure-hub-provisioning") {
		t.Fatalf("missing installer: %v", err)
	}
}

func stepNames(rc provision.Recipe) []string {
	var names []string
	for _, s := range rc {
		names = append(names, s.Name)
	}
	return names
}

// The API's recipe: one Hub address list for the firewall and the agent guard (st.Exit.HubIPs),
// the Hub's own mTLS agent check and the sync hook wired, MTProxy only when not switched off.
func TestApiRecipe(t *testing.T) {
	hub := []string{"198.51.100.1"}
	withExitWiring(t, hub, mtSettings, "x")
	n := provision.Node{ID: 3, Role: "exit", IP: "203.0.113.9", SSHPort: 22, Lifecycle: "online", MTProtoStatus: "healthy", HostKeyConfirmed: true}
	rc, st, err := apiRecipe(context.Background(), n, provision.KindReprovision)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(st.Exit.HubIPs, hub) || st.Hooks.ProbeAgent == nil || st.Hooks.RequestSync == nil || st.Hooks.MarkMTProto == nil {
		t.Fatalf("state: hub %v, hooks %+v", st.Exit.HubIPs, st.Hooks)
	}
	if !slices.Contains(stepNames(rc), "mtproxy") {
		t.Fatalf("steps %v", stepNames(rc))
	}
	n.MTProtoStatus = "disabled"
	if rc, _, err = apiRecipe(context.Background(), n, provision.KindReprovision); err != nil || slices.Contains(stepNames(rc), "mtproxy") {
		t.Fatalf("disabled MTProxy: %v %v", stepNames(rc), err)
	}
	// Without a Hub address no setup job is created; taking a node out of service needs none.
	withExitWiring(t, nil, mtSettings, "x")
	if _, _, err := apiRecipe(context.Background(), n, provision.KindReprovision); !provision.IsRefusal(err) {
		t.Fatalf("setup without Hub addresses must be a refusal (the owner's text): %v", err)
	}
	if rc, _, err := apiRecipe(context.Background(), n, provision.KindDeprovision); err != nil || stepNames(rc)[0] != "exclude" {
		t.Fatalf("deprovision: %v %v", stepNames(rc), err)
	}
}

// The relay's DNAT target is main_server_host — never another of the Hub's interface addresses
// (hubOwnIPs also lists one filtered upstream); SSH may come from all of them. No usable address
// is a refusal (no job), a failed read an internal error; taking a relay out needs no address.
func TestApiRelayOptions(t *testing.T) {
	hubAll := []string{"198.51.100.2", "192.0.2.10"} // 192.0.2.10: an interface address, never the target
	withExitWiring(t, hubAll, mtSettings, "x")
	oldHost, oldLookup := relayHubHost, relayHubLookup
	t.Cleanup(func() { relayHubHost, relayHubLookup = oldHost, oldLookup })
	setHost := func(v string, err error) { relayHubHost = func(context.Context) (string, error) { return v, err } }
	relayHubLookup = func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == "hub.example.test" {
			return []netip.Addr{netip.MustParseAddr("198.51.100.2")}, nil
		}
		return nil, errors.New("no such host")
	}
	ctx := context.Background()
	n := provision.Node{ID: 5, Role: "relay", IP: "203.0.113.50", SSHPort: 22, Lifecycle: "pending", HostKeyConfirmed: true}
	for _, host := range []string{"198.51.100.2", "hub.example.test"} {
		setHost(host, nil)
		rc, st, err := apiRecipe(ctx, n, provision.KindProvision)
		if err != nil || st.Relay.HubIP != "198.51.100.2" || st.Relay.RelayIP != n.IP || !slices.Equal(st.Relay.HubIPs, hubAll) ||
			st.Relay.VerifyPath == nil || len(rc) == 0 || rc[0].Name != "relay_preflight" || rc[len(rc)-1].Name != "activate" {
			t.Fatalf("%s: %+v %v %v", host, st, stepNames(rc), err)
		}
	}
	for name, c := range map[string]struct {
		v   string
		err error
	}{"absent": {"", sql.ErrNoRows}, "blank": {" ", nil}, "private": {"10.0.0.1", nil}, "unresolvable": {"nohost.example.test", nil}} {
		setHost(c.v, c.err)
		if _, _, err := apiRecipe(ctx, n, provision.KindProvision); !provision.IsRefusal(err) {
			t.Errorf("%s: want a refusal, got %v", name, err)
		}
	}
	setHost("", errors.New("db down"))
	if _, _, err := apiRecipe(ctx, n, provision.KindProvision); err == nil || provision.IsRefusal(err) {
		t.Fatalf("a failed read is internal, not the owner's text: %v", err)
	}
	n.Lifecycle = "online"
	if rc, _, err := apiRecipe(ctx, n, provision.KindDeprovision); err != nil || !slices.Contains(stepNames(rc), "relay_teardown") {
		t.Fatalf("deprovision: %v %v", stepNames(rc), err)
	}
}

// «Переустановить Telegram-прокси» / the backfill: a one-step MTProxy job, no Hub address list
// needed; refused (no job) for a node with an unconfirmed key or MTProxy switched off.
func TestApiRecipeMTProxyJob(t *testing.T) {
	withExitWiring(t, nil, mtSettings, "x")
	n := provision.Node{ID: 3, Role: "exit", IP: "203.0.113.9", SSHPort: 22, Lifecycle: "online", MTProtoStatus: "unhealthy", HostKeyConfirmed: true}
	rc, st, err := apiRecipe(context.Background(), n, provision.KindMTProxy)
	if err != nil || !slices.Equal(stepNames(rc), []string{"mtproxy"}) || st.Exit.MTProto == nil || st.Exit.MTProto.Port != 9443 {
		t.Fatalf("mtproxy job: %v %v", stepNames(rc), err)
	}
	n.HostKeyConfirmed = false
	if _, _, err := apiRecipe(context.Background(), n, provision.KindMTProxy); !errors.Is(err, provision.ErrHostKeyNotConfirmed) {
		t.Fatalf("unconfirmed key: %v", err)
	}
	n.HostKeyConfirmed, n.MTProtoStatus = true, "disabled"
	if _, _, err := apiRecipe(context.Background(), n, provision.KindMTProxy); !errors.Is(err, provision.ErrMTProxyDisabled) {
		t.Fatalf("disabled: %v", err)
	}
}

func TestMTProxyBackfill(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	var started []int
	var logs []string
	last := map[int][]provision.JobView{
		2: {{ID: 1, Kind: provision.KindMTProxy, Status: provision.JobFailed, CreatedAt: now.Add(-time.Hour)}},      // retried too soon
		3: {{ID: 2, Kind: provision.KindProvision, Status: provision.JobSucceeded, CreatedAt: now.Add(-time.Hour)}}, // refused by the recipe
		4: {{ID: 3, Kind: provision.KindMTProxy, Status: provision.JobFailed, CreatedAt: now.Add(-7 * time.Hour)}},
		6: {{ID: 4, Kind: provision.KindVerify, Status: provision.JobRunning, CreatedAt: now.Add(-48 * time.Hour)}}, // busy
	}
	b := mtproxyBackfill{
		start: func(_ context.Context, id int, kind string) (int64, error) {
			if kind != provision.KindMTProxy {
				t.Fatalf("kind %s", kind)
			}
			if id == 3 {
				return 0, provision.ErrHostKeyNotConfirmed
			}
			started = append(started, id)
			return 10, nil
		},
		jobs: func(_ context.Context, id, _ int) ([]provision.JobView, error) { return last[id], nil },
		now:  func() time.Time { return now },
		logf: func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) },
	}
	// A node whose latest job still runs is skipped silently (no job, no log line).
	if got := b.run(context.Background(), []int{6}); got != 0 || len(started) != 0 || len(logs) != 0 {
		t.Fatalf("busy node: started %v logs %v", started, logs)
	}
	if got := b.run(context.Background(), []int{2, 3, 4, 5}); got != 4 || !slices.Equal(started, []int{4}) {
		t.Fatalf("started %v (returned %d), want only node 4 — one job per tick", started, got)
	}
	started = nil
	if got := b.run(context.Background(), []int{2, 3}); got != 0 || len(started) != 0 {
		t.Fatalf("nothing to start: %v", started)
	}
}

// No node code dials SSH without the confirmed key; nothing in the API restarts the API.
func TestNoUnpinnedSSHAnywhere(t *testing.T) {
	selfRestart := regexp.MustCompile(`exec\.Command(Context)?\([^)]*heyearth-api`)
	for _, dir := range []string{".", "../../internal/biz", "../../internal/admin", "../../internal/provision"} {
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, _ := os.ReadFile(f)
			if strings.Contains(string(b), "InsecureIgnoreHostKey") || selfRestart.Match(b) {
				t.Errorf("%s: unpinned SSH or API self-restart", f)
			}
		}
	}
	for _, gone := range []string{"../../internal/biz/exit_teardown.go", "../../internal/admin/ssh_tofu.go"} {
		if _, err := os.Stat(gone); err == nil {
			t.Errorf("%s (unpinned SSH) must be gone", gone)
		}
	}
}

func outboundsByTag(t *testing.T, cfg []byte) map[string]json.RawMessage {
	t.Helper()
	var c struct {
		Outbounds []json.RawMessage `json:"outbounds"`
	}
	if err := json.Unmarshal(cfg, &c); err != nil {
		t.Fatal(err)
	}
	m := map[string]json.RawMessage{}
	for _, o := range c.Outbounds {
		var tag struct{ Tag string }
		_ = json.Unmarshal(o, &tag)
		m[tag.Tag] = o
	}
	return m
}

type vlessUser struct {
	ID   string `json:"id"`
	Flow string `json:"flow"`
}

type vlessOutbound struct {
	Settings struct {
		Vnext []struct {
			Address string      `json:"address"`
			Port    int         `json:"port"`
			Users   []vlessUser `json:"users"`
		} `json:"vnext"`
	} `json:"settings"`
	StreamSettings struct {
		Network         string `json:"network"`
		Security        string `json:"security"`
		RealitySettings struct {
			Fingerprint string `json:"fingerprint"`
			ServerName  string `json:"serverName"`
			PublicKey   string `json:"publicKey"`
			ShortID     string `json:"shortId"`
		} `json:"realitySettings"`
	} `json:"streamSettings"`
}

// The wizard's TCP path check dials exactly what Xray's exit-<id>.vr outbound (phase 1) dials.
func TestVRProbeMatchesXrayOutbound(t *testing.T) {
	ex := testExit(2, "hysteria2")
	ex.TunnelSNI = config.ExitRealitySNI
	ex.TunnelPort = 50443 // production: tunnel_port is the hysteria port, the .vr path must still dial :10443
	in := baseInputs()
	in.exitNodes = []biz.ExitNodeTunnel{ex}
	cfg, _ := genConfig(t, in, quotaCutoff{})
	_, vrTag := exitOutboundTags(2)
	raw, ok := outboundsByTag(t, cfg)[vrTag]
	if !ok {
		t.Fatalf("no %s outbound", vrTag)
	}
	probeCfg, err := config.ExitRealityProbeConfig(config.VRProbeSocksPort(2), config.RealityProbe{ExitIP: ex.IPAddress,
		UUID: ex.TunnelUUID, PublicKey: ex.TunnelPubKey, ShortID: ex.TunnelShortID, SNI: ex.TunnelSNI})
	if err != nil {
		t.Fatal(err)
	}
	var probe struct {
		Outbounds []vlessOutbound `json:"outbounds"`
	}
	var x vlessOutbound
	if json.Unmarshal(probeCfg, &probe) != nil || json.Unmarshal(raw, &x) != nil || len(probe.Outbounds) != 1 {
		t.Fatal("unparsable")
	}
	p := probe.Outbounds[0]
	if len(p.Settings.Vnext) != 1 || len(x.Settings.Vnext) != 1 || len(p.Settings.Vnext[0].Users) != 1 || len(x.Settings.Vnext[0].Users) != 1 {
		t.Fatalf("xray %+v\nprobe %+v", x, p)
	}
	xv, pv := x.Settings.Vnext[0], p.Settings.Vnext[0]
	if xv.Port != config.ExitRealityPort || xv.Users[0].Flow != config.ExitRealityFlow || x.StreamSettings.Network != "tcp" ||
		xv.Address != pv.Address || xv.Port != pv.Port || xv.Users[0] != pv.Users[0] ||
		x.StreamSettings.Network != p.StreamSettings.Network || x.StreamSettings.Security != p.StreamSettings.Security ||
		x.StreamSettings.RealitySettings != p.StreamSettings.RealitySettings {
		t.Fatalf("xray %+v\nprobe %+v", x, p)
	}
}

// The owner's TCP-fallback switch (servers.tunnel_fallback_enabled) drops exactly that exit's
// exit-<id>.vr and its probe pair; its Hysteria path, the other exits and legacy VLESS exits
// (TCP is their only path) are untouched.
func TestFallbackSwitchDropsVROutbound(t *testing.T) {
	resetExitNotes(t)
	on, off, legacy := testExit(2, "hysteria2"), testExit(3, "hysteria2"), legacyVLESSExit(4)
	off.FallbackEnabled = false
	legacy.FallbackEnabled = false
	in := baseInputs()
	in.exitNodes = []biz.ExitNodeTunnel{on, off, legacy}
	cfg, _ := genConfig(t, in, quotaCutoff{})
	tags := outboundsByTag(t, cfg)
	_, vr2 := exitOutboundTags(2)
	hy3, vr3 := exitOutboundTags(3)
	_, vr4 := exitOutboundTags(4)
	if _, ok := tags[vr2]; !ok {
		t.Error("exit 2 lost its TCP fallback")
	}
	if _, ok := tags[vr3]; ok {
		t.Error("exit 3 has the fallback switched off")
	}
	if _, ok := tags[exitProbeTag(3)]; ok {
		t.Error("exit 3: no probe outbound for a switched-off path")
	}
	if _, ok := tags[hy3]; !ok {
		t.Error("exit 3 must keep its hysteria path")
	}
	if _, ok := tags[vr4]; !ok {
		t.Error("a legacy VLESS exit's only path is not a fallback: the switch must not drop it")
	}
	if v := viewOf(t, cfg); byTag(v.Inbounds, exitProbeTag(3)) != nil || byTag(v.Inbounds, exitProbeTag(2)) == nil {
		t.Error("probe inbounds: exit 3 must have none, exit 2 its own")
	}
}

func TestLegacyPasswordAction(t *testing.T) {
	good := func([]byte) (string, error) { return "pw", nil }
	bad := func([]byte) (string, error) { return "", errors.New("auth failed") }
	for _, tc := range []struct {
		enc  []byte
		dec  func([]byte) (string, error)
		want string
	}{{nil, good, "encrypt"}, {[]byte{}, good, "encrypt"}, {[]byte{1}, good, "clear"}, {[]byte{1}, bad, "keep"}} {
		if got := legacyPasswordAction(tc.enc, tc.dec); got != tc.want {
			t.Errorf("%v: %s, want %s", tc.enc, got, tc.want)
		}
	}
}

type legacyExec struct {
	query string
	args  []any
}

// The migration writes each row only while it is as read, empties the NOT NULL plaintext column
// (the empty string, never NULL), keeps a plaintext whose ciphertext does not decrypt, and logs
// no password.
func TestMigrateLegacySSHPasswords(t *testing.T) {
	var execs []legacyExec
	affected := map[int]int64{1: 1, 2: 1, 4: 0}
	exec := func(_ context.Context, q string, args ...any) (int64, error) {
		execs = append(execs, legacyExec{q, args})
		return affected[args[0].(int)], nil
	}
	encrypt := func(p string) ([]byte, error) {
		if p == "plain-secret-5" {
			return nil, errors.New("encryption unavailable")
		}
		return []byte("enc:" + p), nil
	}
	decrypt := func(b []byte) (string, error) {
		if string(b) == "readable" {
			return "plain-secret-2", nil
		}
		return "", errors.New("crypto: decrypt: cipher: message authentication failed")
	}
	var logs bytes.Buffer
	logf := func(f string, a ...any) { fmt.Fprintf(&logs, f+"\n", a...) }
	migrateLegacyPasswords(context.Background(), []legacyPassword{
		{id: 1, plain: "plain-secret-1"},                          // plaintext only: encrypt
		{id: 2, plain: "plain-secret-2", enc: []byte("readable")}, // readable ciphertext: drop the plaintext
		{id: 3, plain: "plain-secret-3", enc: []byte("foreign")},  // another key's ciphertext: keep both
		{id: 4, plain: "plain-secret-4"},                          // changed meanwhile: the guarded write hits nothing
		{id: 5, plain: "plain-secret-5"},                          // encryption fails: nothing written
	}, exec, encrypt, decrypt, logf)

	if len(execs) != 3 {
		t.Fatalf("writes %+v, want rows 1, 2, 4", execs)
	}
	if e := execs[0]; e.query != encryptLegacyPasswordQuery || e.args[0] != 1 || e.args[1] != "plain-secret-1" || string(e.args[2].([]byte)) != "enc:plain-secret-1" {
		t.Errorf("row 1: %+v", e)
	}
	if e := execs[1]; e.query != clearLegacyPasswordQuery || e.args[0] != 2 || e.args[1] != "plain-secret-2" || string(e.args[2].([]byte)) != "readable" {
		t.Errorf("row 2: %+v", e)
	}
	if e := execs[2]; e.query != encryptLegacyPasswordQuery || e.args[0] != 4 {
		t.Errorf("row 4: %+v", e)
	}
	out := logs.String()
	if strings.Contains(out, "plain-secret") || strings.Contains(out, "enc:") || strings.Contains(out, "readable") {
		t.Fatalf("a password reached the log:\n%s", out)
	}
	for _, want := range []string{"сервер 1: открытый пароль SSH зашифрован", "сервер 2: открытая копия", "сервер 3: зашифрованный пароль не читается",
		"сервер 4: строка изменилась", "сервер 5: не удалось зашифровать"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
	// ssh_password is NOT NULL DEFAULT '' (000004); each write is guarded by what was read.
	nullPlain := regexp.MustCompile(`(?i)ssh_password\s*=\s*NULL`)
	for _, q := range []string{encryptLegacyPasswordQuery, clearLegacyPasswordQuery} {
		if nullPlain.MatchString(q) || !strings.Contains(q, "ssh_password = ''") ||
			!strings.Contains(q, "ssh_password = $2") || !strings.Contains(q, "id = $1") {
			t.Errorf("query %s", q)
		}
	}
	if !strings.Contains(encryptLegacyPasswordQuery, "COALESCE(octet_length(ssh_password_enc), 0) = 0") ||
		!strings.Contains(clearLegacyPasswordQuery, "ssh_password_enc = $3") {
		t.Error("the writes must re-check the ciphertext they decided on")
	}
	if !strings.Contains(legacyPasswordsQuery, "ssh_password <> ''") {
		t.Errorf("read: %s", legacyPasswordsQuery)
	}
}

// No SSH_ENCRYPTION_KEY (or a bad one): a critical alert that says what to run; a good key resolves it.
func TestSSHEncryptionKeyAlert(t *testing.T) {
	ah := withExitAlertHarness(t)
	if checkSSHEncryptionKey(func() error { return errors.New("SSH_ENCRYPTION_KEY env not set") }) {
		t.Fatal("a missing key reported as ready")
	}
	if len(ah.events) != 1 || ah.events[0].Kind != "raise" || ah.events[0].Key != sshKeyAlertKey || ah.events[0].Level != AlertCritical ||
		!strings.Contains(ah.events[0].Details, "--ensure-hub-provisioning") || !strings.Contains(ah.events[0].Details, "env not set") {
		t.Fatalf("alerts: %+v", ah.events)
	}
	if !checkSSHEncryptionKey(func() error { return nil }) {
		t.Fatal("a valid key reported as missing")
	}
	if got := alertLog(ah); got != "raise "+sshKeyAlertKey+", resolve "+sshKeyAlertKey {
		t.Fatalf("alerts: %s", got)
	}
}

// A valid but DIFFERENT SSH_ENCRYPTION_KEY passes crypto.Ready: the stored ciphertexts it cannot
// read are counted at start and reported (warning, with the server ids — no secret); none
// unreadable resolves the alert.
func TestUnreadableSSHCiphertexts(t *testing.T) {
	decrypt := func(b []byte) (string, error) {
		if string(b) == "ours" {
			return "pw", nil
		}
		return "", errors.New("crypto: decrypt: cipher: message authentication failed")
	}
	got := unreadableCiphertexts([]storedCipher{{2, []byte("ours")}, {3, []byte("foreign")}, {9, []byte("foreign-too")}}, decrypt)
	if !slices.Equal(got, []int{3, 9}) {
		t.Fatalf("unreadable %v, want [3 9]", got)
	}

	ah := withExitAlertHarness(t)
	reportUnreadableCiphertexts(got)
	if len(ah.events) != 1 || ah.events[0].Kind != "raise" || ah.events[0].Key != sshCiphertextsAlertKey || ah.events[0].Level != AlertWarn {
		t.Fatalf("alerts: %+v", ah.events)
	}
	if e := ah.events[0]; !strings.Contains(e.Title, "2") || !strings.Contains(e.Details, "3, 9") || strings.Contains(e.Title+e.Details, "foreign") {
		t.Fatalf("alert text: %q / %q", e.Title, e.Details)
	}
	reportUnreadableCiphertexts(nil)
	if got := alertLog(ah); got != "raise "+sshCiphertextsAlertKey+", resolve "+sshCiphertextsAlertKey {
		t.Fatalf("alerts: %s", got)
	}
	if q := strings.Join(strings.Fields(storedCiphertextsQuery), " "); !strings.Contains(q, "COALESCE(octet_length(ssh_password_enc), 0) > 0") {
		t.Errorf("query: %s", q)
	}
}

// B7: an artifact whose sha256 file disagrees is refused; one without the file is accepted.
func TestArtifactChecksum(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "node-agent")
	if err := os.WriteFile(p, []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readHubFile(p); err != nil {
		t.Fatalf("no checksum file: %v", err)
	}
	// sha256("binary")
	if err := os.WriteFile(p+".sha256", []byte("9a3a45d01531a20e89ac6ae10b0b0beb0492acd7216a368aa062d1a5fecaf9cd\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readHubFile(p); err != nil {
		t.Fatalf("matching checksum: %v", err)
	}
	if err := os.WriteFile(p, []byte("binar"), 0o600); err != nil { // cut off
		t.Fatal(err)
	}
	if _, err := readHubFile(p); err == nil || !strings.Contains(err.Error(), "повреждён") {
		t.Fatalf("cut-off file: %v", err)
	}
}
