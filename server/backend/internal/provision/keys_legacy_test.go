package provision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/config"
)

// legacyKeysJSON is the old deploy's keys file as measured on exit 2 (2026-10-26): the Reality key
// pair and the short id only — the tunnel UUID and password live in the exit's xray and hysteria
// configs and in servers.tunnel_*.
func legacyKeysJSON(k config.ExitRealityKeys) string {
	return fmt.Sprintf("{\n  \"privateKey\": %q,\n  \"publicKey\": %q,\n  \"shortId\": %q\n}", k.PrivateKey, k.PublicKey, k.ShortID)
}

func testKeys(t *testing.T) config.ExitRealityKeys {
	t.Helper()
	k, err := completeKeys(config.ExitRealityKeys{})
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// otherKeys: a second, unrelated set of tunnel credentials.
func otherKeys(t *testing.T, k config.ExitRealityKeys) config.ExitRealityKeys {
	t.Helper()
	o := testKeys(t)
	if o.UUID == k.UUID || o.SSPassword == k.SSPassword {
		t.Fatal("random keys collided")
	}
	return o
}

// legacyKeysNode is an exit with the old keys file (0600) whose running configs carry the tunnel
// UUID (the Reality inbound's client) and password (hysteria auth) of k.
func legacyKeysNode(t *testing.T, k config.ExitRealityKeys) *fakeHost {
	t.Helper()
	n := newFakeHost()
	xc, err := legacyXrayConfigJSON(k, "203.0.113.7", 8443)
	if err != nil {
		t.Fatal(err)
	}
	n.files[config.ExitKeysPath], n.modes[config.ExitKeysPath] = []byte(legacyKeysJSON(k)), 0o600
	n.files[config.ExitXrayConfig], n.modes[config.ExitXrayConfig] = xc, 0o600
	n.files[config.ExitHysteriaConfig] = []byte(config.ExitHysteriaServerYAML(k.SSPassword, "203.0.113.7", 8443))
	n.modes[config.ExitHysteriaConfig] = 0o600
	return n
}

// legacyKeysState: a run whose database row holds db; loads counts LoadTunnel calls.
func legacyKeysState(kind string, db Tunnel, loads *int) *State {
	h := (&fakeHooks{lifecycle: "online", tunnel: db}).Hooks()
	load := h.LoadTunnel
	h.LoadTunnel = func(ctx context.Context, id int) (Tunnel, error) {
		if loads != nil {
			*loads++
		}
		return load(ctx, id)
	}
	st := NewState(kind, Node{ID: 7, IP: "203.0.113.7"}, nil, h)
	st.out = &tailBuffer{max: 1 << 16}
	return st
}

func dbTunnel(k config.ExitRealityKeys) Tunnel {
	return Tunnel{UUID: k.UUID, PublicKey: k.PublicKey, ShortID: k.ShortID, SNI: config.ExitRealitySNI, Password: k.SSPassword}
}

func fileKeys(t *testing.T, n *fakeHost) config.ExitRealityKeys {
	t.Helper()
	var k config.ExitRealityKeys
	if err := json.Unmarshal(n.files[config.ExitKeysPath], &k); err != nil {
		t.Fatal(err)
	}
	return k
}

// noSecrets fails when any of the values shows up in any of the texts.
func noSecrets(t *testing.T, texts []string, secrets ...string) {
	t.Helper()
	for _, txt := range texts {
		for _, sec := range secrets {
			if sec != "" && strings.Contains(txt, sec) {
				t.Fatalf("secret %q leaked into %q", sec, txt)
			}
		}
	}
}

// Exit 2's keys file of the old format: verify reports it as drift that keeps the values (not as
// a broken UUID) and takes the database's values, which agree with the running xray and
// hysteria; apply rewrites the file in the full format with exactly those values — nothing new is
// generated — and a second pass changes nothing.
func TestLegacyKeysAdoptedFromDatabase(t *testing.T) {
	ctx := context.Background()
	k := testKeys(t)
	node := legacyKeysNode(t, k)
	legacy := string(node.files[config.ExitKeysPath])

	st := legacyKeysState(KindVerify, dbTunnel(k), nil)
	err := keysVerify(ctx, node, st)
	if err == nil || !strings.HasPrefix(err.Error(), legacyKeysDrift) || strings.Contains(err.Error(), "некорректен") ||
		!strings.Contains(err.Error(), "UUID туннеля — из базы (servers.tunnel_uuid), совпадает с id клиента Reality (inbound :10443) в "+config.ExitXrayConfig) ||
		!strings.Contains(err.Error(), "пароль туннеля — из базы (servers.tunnel_ss_password), совпадает с «password:» блока auth в "+config.ExitHysteriaConfig) {
		t.Fatalf("verify: %v", err)
	}
	if st.Keys != k || node.writes != 0 || string(node.files[config.ExitKeysPath]) != legacy {
		t.Fatalf("verify must take the adopted keys and write nothing: keys ok %v, writes %d", st.Keys == k, node.writes)
	}
	for _, p := range []string{config.ExitXrayConfig, config.ExitHysteriaConfig, config.ExitHysteriaConfigLegacy} {
		if node.count("cat "+p) != 1 {
			t.Errorf("the live copy %s was not cross-checked", p)
		}
	}
	if sec := st.Secrets(); !slices.Contains(sec, k.UUID) || !slices.Contains(sec, k.SSPassword) {
		t.Fatal("the adopted values must be secrets of the run")
	}
	noSecrets(t, []string{err.Error(), st.out.String()}, k.UUID, k.SSPassword, k.PrivateKey)

	st = legacyKeysState(KindReprovision, dbTunnel(k), nil)
	if err := keysApply(ctx, node, st); err != nil {
		t.Fatal(err)
	}
	if got := fileKeys(t, node); got != k || st.Keys != k {
		t.Fatalf("the file must carry the same values (no rotation): %v", got == k)
	}
	if node.writes != 1 || node.modes[config.ExitKeysPath] != 0o600 || string(node.files[config.ExitKeysPath+".bak"]) != legacy ||
		node.modes[config.ExitKeysPath+".bak"]&0o077 != 0 {
		t.Fatalf("writes %d mode %o .bak mode %o", node.writes, node.modes[config.ExitKeysPath], node.modes[config.ExitKeysPath+".bak"])
	}
	if out := st.out.String(); !strings.Contains(out, "файл ключей старого формата переписан в полный") {
		t.Fatalf("apply output: %q", out)
	}
	noSecrets(t, []string{st.out.String()}, k.UUID, k.SSPassword, k.PrivateKey)

	// Converged: the full file verifies, the database is not even asked, apply writes nothing.
	loads := 0
	st = legacyKeysState(KindReprovision, dbTunnel(k), &loads)
	if err := keysVerify(ctx, node, st); err != nil || st.Keys != k {
		t.Fatalf("after apply: %v", err)
	}
	if err := keysApply(ctx, node, st); err != nil || node.writes != 1 || loads != 0 {
		t.Fatalf("second apply: %v, writes %d, database reads %d", err, node.writes, loads)
	}
}

// The exact places the refusals and notes name (the owner compares them by hand).
var (
	wantXrayWhere    = "id клиента Reality (inbound :10443) в " + config.ExitXrayConfig
	wantHyWhere      = "«password:» блока auth в " + config.ExitHysteriaConfig
	wantHyEtcWhere   = "«password:» блока auth в " + config.ExitHysteriaConfigLegacy
	wantUUIDInDB     = "UUID туннеля в базе (servers.tunnel_uuid)"
	wantPasswordInDB = "пароль туннеля в базе (servers.tunnel_ss_password)"
)

// A disagreement between the database and the exit (or among the exit's own copies), a value
// that is not a valid credential, or a live config that is there but cannot be read refuses — in
// verify and apply alike: no value is picked, none generated, the file stays as it is, and the
// error names the exact places to compare, never a value or the content.
func TestLegacyKeysRefusedOnMismatch(t *testing.T) {
	ctx := context.Background()
	k := testKeys(t)
	o := otherKeys(t, k)
	twoClients := fmt.Sprintf(`{"inbounds": [{"tag": "vless-reality", "port": 10443, "protocol": "vless",
  "settings": {"clients": [{"id": %q}, {"id": %q}]}}]}`, k.UUID, o.UUID)
	for name, c := range map[string]struct {
		db    Tunnel
		setup func(n *fakeHost)
		want  string
	}{
		"uuid: database ≠ xray": {db: Tunnel{UUID: o.UUID, Password: k.SSPassword},
			want: wantUUIDInDB + " не совпадает с " + wantXrayWhere},
		"password: database ≠ hysteria": {db: Tunnel{UUID: k.UUID, Password: o.SSPassword},
			want: wantPasswordInDB + " не совпадает с " + wantHyWhere},
		"password: database ≠ the copy in /etc/hysteria": {db: dbTunnel(k),
			setup: func(n *fakeHost) {
				n.files[config.ExitHysteriaConfigLegacy] = []byte("listen: :50443\nauth:\n  type: password\n  password: " + o.SSPassword + "\n")
			},
			want: wantPasswordInDB + " не совпадает с " + wantHyEtcWhere},
		"uuid: no database value, two different live clients": {db: Tunnel{Password: k.SSPassword},
			setup: func(n *fakeHost) { n.files[config.ExitXrayConfig] = []byte(twoClients) },
			want:  wantUUIDInDB + " пуст, а на ноде разные значения: " + wantXrayWhere},
		"password: no database value, the exit's copies differ": {db: Tunnel{UUID: k.UUID},
			setup: func(n *fakeHost) {
				n.files[config.ExitHysteriaConfigLegacy] = []byte("auth:\n  type: password\n  password: " + o.SSPassword + "\n")
			},
			want: wantPasswordInDB + " пуст, а на ноде разные значения: " + wantHyWhere + ", " + wantHyEtcWhere},
		"uuid: malformed database value, nothing live": {db: Tunnel{UUID: "AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE", Password: k.SSPassword},
			setup: func(n *fakeHost) { n.remove(config.ExitXrayConfig) },
			want:  wantUUIDInDB + " некорректен"},
		// Unreadable live copies: never "no copy" (that would create a new value without a
		// database one, and skip the cross-check with it).
		"xray config not JSON, no database value": {db: Tunnel{Password: k.SSPassword},
			setup: func(n *fakeHost) { n.files[config.ExitXrayConfig] = []byte(`{"inbounds": [ not json ` + k.UUID) },
			want:  config.ExitXrayConfig + " на ноде не разобран (содержимое не показано) — UUID туннеля по нему не сверить"},
		"xray config not JSON, database value": {db: dbTunnel(k),
			setup: func(n *fakeHost) { n.files[config.ExitXrayConfig] = []byte("inbounds = [] # toml?") },
			want:  config.ExitXrayConfig + " на ноде не разобран"},
		"hysteria: another auth type": {db: Tunnel{UUID: k.UUID},
			setup: func(n *fakeHost) {
				n.files[config.ExitHysteriaConfig] = []byte("auth:\n  type: userpass\n  userpass:\n    hub: " + k.SSPassword + "\n")
			},
			want: config.ExitHysteriaConfig + " на ноде не разобран (содержимое не показано) — пароль туннеля по нему не сверить"},
		"hysteria: /etc copy without auth": {db: dbTunnel(k),
			setup: func(n *fakeHost) {
				n.files[config.ExitHysteriaConfigLegacy] = []byte("listen: :50443\nmasquerade:\n  type: proxy\n")
			},
			want: config.ExitHysteriaConfigLegacy + " на ноде не разобран"},
		"hysteria: unterminated quote": {db: Tunnel{UUID: k.UUID},
			setup: func(n *fakeHost) {
				n.files[config.ExitHysteriaConfig] = []byte("auth:\n  type: password\n  password: \"" + k.SSPassword + "\n")
			},
			want: config.ExitHysteriaConfig + " на ноде не разобран"},
	} {
		t.Run(name, func(t *testing.T) {
			node := legacyKeysNode(t, k)
			if c.setup != nil {
				c.setup(node)
			}
			legacy := string(node.files[config.ExitKeysPath])
			for _, kind := range []string{KindVerify, KindReprovision} {
				st := legacyKeysState(kind, c.db, nil)
				f := keysVerify
				if kind == KindReprovision {
					f = keysApply
				}
				err := f(ctx, node, st)
				if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "не перенимаю и новый не создаю") ||
					strings.Contains(err.Error(), legacyKeysDrift) || inconclusive(ctx, err) {
					t.Fatalf("%s: %v", kind, err)
				}
				if st.Keys != (config.ExitRealityKeys{}) {
					t.Fatalf("%s: a refused run must not take keys", kind)
				}
				noSecrets(t, []string{err.Error(), st.out.String()}, k.UUID, k.SSPassword, o.UUID, o.SSPassword, k.PrivateKey,
					"not json", "toml", "userpass", "masquerade")
			}
			if node.writes != 0 || string(node.files[config.ExitKeysPath]) != legacy {
				t.Fatalf("refused, yet the keys file changed (writes %d)", node.writes)
			}
		})
	}
}

// Where the database has no value the one the exit runs with is taken; where the exit has no copy
// the database's is; a value found nowhere is created (there is no tunnel to keep) and verify says
// so instead of promising to keep it. Live copies are read in the forms xray and hysteria accept:
// JSON with comments, a YAML flow map, quotes and comments.
func TestLegacyKeysOtherSources(t *testing.T) {
	ctx := context.Background()
	k := testKeys(t)
	quoted := "# by hand\nlisten: :50443\nauth:\n  type: password # one shared password\n  password: \"" + k.SSPassword + "\"  # tunnel\n"
	commented := fmt.Sprintf(`// xray, edited by hand
{
  /* the Hub's TCP fallback */
  "inbounds": [{"tag": "vless-reality", "port": "10443", // a string port
    "settings": {"clients": [{"id": %q, "flow": "xtls-rprx-vision"}]}}]
}`, k.UUID)
	flow := "listen: :50443\nauth: {type: password, password: '" + k.SSPassword + "'} # tunnel\n"
	for name, c := range map[string]struct {
		db       Tunnel
		setup    func(n *fakeHost)
		notes    []string
		generate bool // UUID and password are created
	}{
		"database only, no live copy": {db: dbTunnel(k), setup: func(n *fakeHost) { n.remove(config.ExitXrayConfig, config.ExitHysteriaConfig) },
			notes: []string{"UUID туннеля — из базы (servers.tunnel_uuid); на ноде его копии нет, сверить не с чем",
				"пароль туннеля — из базы (servers.tunnel_ss_password); на ноде его копии нет, сверить не с чем"}},
		"live only, database empty": {
			notes: []string{"UUID туннеля — с ноды (" + wantXrayWhere + "); в базе (servers.tunnel_uuid) пусто",
				"пароль туннеля — с ноды (" + wantHyWhere + "); в базе (servers.tunnel_ss_password) пусто"}},
		"password only in /etc/hysteria, quoted": {db: dbTunnel(k), setup: func(n *fakeHost) {
			n.remove(config.ExitHysteriaConfig)
			n.files[config.ExitHysteriaConfigLegacy] = []byte(quoted)
		}, notes: []string{"пароль туннеля — из базы (servers.tunnel_ss_password), совпадает с " + wantHyEtcWhere}},
		"live only: xray JSON with comments, hysteria flow map": {setup: func(n *fakeHost) {
			n.files[config.ExitXrayConfig] = []byte(commented)
			n.files[config.ExitHysteriaConfig] = []byte(flow)
		}, notes: []string{"UUID туннеля — с ноды (" + wantXrayWhere + ")", "пароль туннеля — с ноды (" + wantHyWhere + ")"}},
		"empty live files are no copies": {db: dbTunnel(k), setup: func(n *fakeHost) {
			n.files[config.ExitXrayConfig], n.files[config.ExitHysteriaConfigLegacy] = []byte(" \n"), []byte("\n")
			n.remove(config.ExitHysteriaConfig)
		}, notes: []string{"UUID туннеля — из базы (servers.tunnel_uuid); на ноде его копии нет"}},
		"nowhere": {setup: func(n *fakeHost) { n.remove(config.ExitXrayConfig, config.ExitHysteriaConfig) }, generate: true,
			notes: []string{"UUID туннеля — нет ни в базе (servers.tunnel_uuid), ни на ноде: будет создан новый",
				"пароль туннеля — нет ни в базе (servers.tunnel_ss_password), ни на ноде: будет создан новый"}},
	} {
		t.Run(name, func(t *testing.T) {
			node := legacyKeysNode(t, k)
			if c.setup != nil {
				c.setup(node)
			}
			st := legacyKeysState(KindVerify, c.db, nil)
			err := keysVerify(ctx, node, st)
			if err == nil {
				t.Fatal("a keys file of the old format must be drift")
			}
			for _, n := range c.notes {
				if !strings.Contains(err.Error(), n) {
					t.Errorf("verify %q lacks %q", err, n)
				}
			}
			if c.generate == strings.Contains(err.Error(), legacyKeysDrift) {
				t.Errorf("verify must promise to keep the values only when it can: %q", err)
			}
			st = legacyKeysState(KindReprovision, c.db, nil)
			if err := keysApply(ctx, node, st); err != nil {
				t.Fatal(err)
			}
			got := fileKeys(t, node)
			if err := validKeys(got); err != nil || got.PrivateKey != k.PrivateKey || got.ShortID != k.ShortID {
				t.Fatalf("%v: the Reality key pair and short id must stay", err)
			}
			if kept := got == k; kept == c.generate {
				t.Fatalf("UUID/password kept %v, want %v", kept, !c.generate)
			}
		})
	}
}

// No keys file (or one without the Reality key pair) on an exit that has a tunnel — in its
// database row or in a live config, readable or not: new keys would cut it, so verify says so and
// apply refuses, writing nothing. Only an exit with no tunnel anywhere gets new keys.
func TestNoKeysFileRefusedWhileATunnelExists(t *testing.T) {
	ctx := context.Background()
	k := testKeys(t)
	noFile := "файла ключей туннеля " + config.ExitKeysPath + " нет, а туннель у этого выхода уже есть"
	for name, c := range map[string]struct {
		db    Tunnel
		setup func(n *fakeHost)
		want  []string
	}{
		"database uuid only":     {db: Tunnel{UUID: k.UUID}, want: []string{noFile, "(в базе servers.tunnel_uuid)"}},
		"database password only": {db: Tunnel{Password: k.SSPassword}, want: []string{noFile, "(в базе servers.tunnel_ss_password)"}},
		"live xray only": {setup: func(n *fakeHost) {
			xc, _ := legacyXrayConfigJSON(k, "203.0.113.7", 8443)
			n.files[config.ExitXrayConfig] = xc
		}, want: []string{noFile, "(на ноде " + wantXrayWhere + ")"}},
		"live /etc hysteria only": {setup: func(n *fakeHost) {
			n.files[config.ExitHysteriaConfigLegacy] = []byte("auth:\n  type: password\n  password: " + k.SSPassword + "\n")
		}, want: []string{noFile, "(на ноде " + wantHyEtcWhere + ")"}},
		"an unreadable live config": {setup: func(n *fakeHost) { n.files[config.ExitHysteriaConfig] = []byte("auth: password\n") },
			want: []string{noFile, "(на ноде " + config.ExitHysteriaConfig + " (не разобран))"}},
		"everything (an online exit whose file was deleted)": {db: dbTunnel(k), setup: func(n *fakeHost) {
			xc, _ := legacyXrayConfigJSON(k, "203.0.113.7", 8443)
			n.files[config.ExitXrayConfig] = xc
			n.files[config.ExitHysteriaConfig] = []byte(config.ExitHysteriaServerYAML(k.SSPassword, "203.0.113.7", 8443))
		}, want: []string{noFile, "в базе servers.tunnel_uuid, servers.tunnel_ss_password; на ноде " + wantXrayWhere + ", " + wantHyWhere,
			"Верните файл ключей из резервной копии"}},
		"a file without the key pair": {db: dbTunnel(k), setup: func(n *fakeHost) {
			n.files[config.ExitKeysPath] = []byte(fmt.Sprintf(`{"uuid": %q, "ssPassword": %q}`, k.UUID, k.SSPassword))
			n.modes[config.ExitKeysPath] = 0o600
		}, want: []string{"в файле ключей туннеля " + config.ExitKeysPath + " нет ключей Reality, а туннель у этого выхода уже есть"}},
	} {
		t.Run(name, func(t *testing.T) {
			node := newFakeHost()
			if c.setup != nil {
				c.setup(node)
			}
			before := maps.Clone(node.files)
			for _, kind := range []string{KindVerify, KindReprovision} {
				st := legacyKeysState(kind, c.db, nil)
				f := keysVerify
				if kind == KindReprovision {
					f = keysApply
				}
				err := f(ctx, node, st)
				if err == nil || inconclusive(ctx, err) || !strings.Contains(err.Error(), "новые ключи разорвали бы его, не создаю") {
					t.Fatalf("%s: %v", kind, err)
				}
				for _, w := range c.want {
					if !strings.Contains(err.Error(), w) {
						t.Fatalf("%s: %q lacks %q", kind, err, w)
					}
				}
				noSecrets(t, []string{err.Error(), st.out.String()}, k.UUID, k.SSPassword, k.PrivateKey)
			}
			if node.writes != 0 || !maps.EqualFunc(node.files, before, func(a, b []byte) bool { return string(a) == string(b) }) {
				t.Fatalf("refused, yet something was written (writes %d)", node.writes)
			}
		})
	}

	// No tunnel anywhere: a fresh exit gets new keys.
	node := newFakeHost()
	st := legacyKeysState(KindProvision, Tunnel{}, nil)
	if err := keysVerify(ctx, node, st); err == nil || err.Error() != "файла ключей туннеля ещё нет" {
		t.Fatalf("verify of a fresh exit: %v", err)
	}
	if err := keysApply(ctx, node, st); err != nil || validKeys(fileKeys(t, node)) != nil || node.modes[config.ExitKeysPath] != 0o600 {
		t.Fatalf("a fresh exit must get keys: %v", err)
	}
}

// A keys file that is already complete is left exactly as it is: no database read, no live
// configs read, no write.
func TestFullKeysFileUntouched(t *testing.T) {
	ctx := context.Background()
	k := testKeys(t)
	node := legacyKeysNode(t, k)
	b, _ := json.MarshalIndent(k, "", "  ")
	full := string(b) + "\n"
	node.files[config.ExitKeysPath] = []byte(full)
	loads := 0
	st := legacyKeysState(KindReprovision, dbTunnel(otherKeys(t, k)), &loads) // a stale row must not matter here
	if err := keysVerify(ctx, node, st); err != nil || st.Keys != k {
		t.Fatalf("verify: %v", err)
	}
	if err := keysApply(ctx, node, st); err != nil || st.Keys != k {
		t.Fatalf("apply: %v", err)
	}
	if loads != 0 || node.count("cat "+config.ExitXrayConfig) != 0 || node.count("cat "+config.ExitHysteriaConfig) != 0 ||
		node.writes != 0 || string(node.files[config.ExitKeysPath]) != full {
		t.Fatalf("database reads %d, live reads %d/%d, writes %d", loads, node.count("cat "+config.ExitXrayConfig),
			node.count("cat "+config.ExitHysteriaConfig), node.writes)
	}
}

// A failed read of a live config is not a reason to generate anything: the error keeps its kind
// (inconclusive: no apply) and never the file's content; a database error refuses too.
func TestLegacyKeysReadErrors(t *testing.T) {
	ctx := context.Background()
	k := testKeys(t)
	node := legacyKeysNode(t, k)
	node.dropAfterOutput = "cat " + config.ExitXrayConfig
	st := legacyKeysState(KindReprovision, dbTunnel(k), nil)
	err := keysApply(ctx, node, st)
	if !errors.Is(err, ErrConnectionLost) || !inconclusive(ctx, err) || node.writes != 0 {
		t.Fatalf("%v (writes %d)", err, node.writes)
	}
	noSecrets(t, []string{err.Error()}, k.UUID, k.PrivateKey)

	node = legacyKeysNode(t, k)
	h := (&fakeHooks{}).Hooks()
	h.LoadTunnel = func(context.Context, int) (Tunnel, error) { return Tunnel{}, errors.New("pq: connection refused") }
	st = NewState(KindReprovision, Node{ID: 7}, nil, h)
	if err := keysApply(ctx, node, st); err == nil || !strings.Contains(err.Error(), "данные туннеля в базе не прочитаны") || node.writes != 0 {
		t.Fatalf("%v (writes %d)", err, node.writes)
	}
}

func TestHysteriaAuthPassword(t *testing.T) {
	const pw = "c2VjcmV0LXR1bm5lbC1wdw=="
	for name, c := range map[string]struct {
		yaml, want string
		ok         bool
	}{
		"template":           {config.ExitHysteriaServerYAML(pw, "203.0.113.7", 8443), pw, true},
		"template, quoted":   {config.ExitHysteriaServerYAML("not base64: x", "203.0.113.7", 0), "not base64: x", true},
		"crlf and comments":  {"# hy\r\nauth:\r\n  # the tunnel\r\n  type: password\r\n  password: " + pw + " # x\r\n", pw, true},
		"single-quoted":      {"auth:\n    type: 'password'\n    password: 'it''s'\n", "it's", true},
		"no auth block":      {"listen: :50443\npassword: " + pw + "\n", "", false},
		"other type":         {"auth:\n  type: userpass\n  userpass:\n    password: " + pw + "\n", "", false},
		"password elsewhere": {"masquerade:\n  password: " + pw + "\nauth:\n  type: password\n", "", false},
		"deeper level only":  {"auth:\n  type: password\n  http:\n    password: " + pw + "\n", "", false},
		"empty":              {"", "", false},
		"broken quote":       {"auth:\n  type: password\n  password: \"abc\n", "", false},
		// Trailing comments, the flow-map form, and what this parser refuses to guess.
		"quoted, comment with quotes": {"auth:\n  type: password\n  password: \"" + pw + "\" # \"old\" one\n", pw, true},
		"plain, # without a blank":    {"auth:\n  type: password\n  password: abc#def # c\n", "abc#def", true},
		"flow map":                    {"listen: :50443\nauth: {type: password, password: " + pw + "} # tunnel\n", pw, true},
		"flow map, quoted":            {"auth: { password: \"a, b}\" , type: \"password\" }\n", "a, b}", true},
		"flow map, empty password":    {"auth: {type: password, password: }\n", "", false},
		"flow map, nested":            {"auth: {type: http, http: {url: http://127.0.0.1:8001}}\n", "", false},
		"flow map, unterminated":      {"auth: {type: password, password: " + pw + "\n", "", false},
		"flow map, junk after":        {"auth: {type: password, password: " + pw + "} x\n", "", false},
		"auth as a scalar":            {"auth: password\n", "", false},
		"two auth keys":               {"auth:\n  type: password\n  password: a\nauth:\n  type: password\n  password: b\n", "", false},
		"anchor":                      {"auth:\n  type: password\n  password: &pw " + pw + "\n", "", false},
		"block scalar":                {"auth:\n  type: password\n  password: |\n    " + pw + "\n", "", false},
	} {
		if got, ok := hysteriaAuthPassword(c.yaml); got != c.want || ok != c.ok {
			t.Errorf("%s: %q %v, want %q %v", name, got, ok, c.want, c.ok)
		}
	}
}

// ── the recipe: downstream steps compare against the adopted values ──

// stlsPassword matches the ShadowTLS password of a backup tunnel config (32 hex characters; the
// Shadowsocks key next to it is base64 of another length).
var stlsPassword = regexp.MustCompile(`"password": "([0-9a-f]{32})"`)

// tieTunnelToCredentials makes the Hub's path probes answer only when both ends carry the same
// credentials, as the real tunnel does: the main tunnel when the Hub client's auth is the exit's
// hysteria password, the backup path when the Hub's ShadowTLS client dials with the password the
// exit's ShadowTLS server expects. probed collects the passwords the backup probes dialled with.
func tieTunnelToCredentials(e *exitEnv, probed *[]string) {
	e.hub.probeDead = func(op string) bool {
		switch op {
		case "socks-probe 1095":
			pw, ok := hysteriaAuthPassword(string(e.node.files[config.ExitHysteriaConfig]))
			hub, hubOK := yamlScalar(between(string(e.hub.files[config.HubClientConfigPath(7)]), "\nauth: ", "\n"))
			return !ok || !hubOK || hub != pw
		case "socks-probe 2095":
			m := stlsPassword.FindStringSubmatch(string(e.hub.files[config.HubSTLSConfigPath(7)]))
			if m == nil {
				return true
			}
			*probed = append(*probed, m[1])
			live := stlsPassword.FindStringSubmatch(string(e.node.files[config.ExitSTLSConfig]))
			return live == nil || live[1] != m[1]
		}
		return false
	}
}

// stlsPW is the ShadowTLS password the tunnel UUID derives.
func stlsPW(t *testing.T, uuid string) string {
	t.Helper()
	pw, _, err := config.STLSSecrets(uuid)
	if err != nil {
		t.Fatal(err)
	}
	return pw
}

// A converged exit whose keys file is (back) in the old format: verify shows that one drift and
// nothing else — every later step compares against the adopted values — and «Переустановить»
// rewrites only the keys file: no restart, no database change, no sync.
func TestExitLegacyKeysFileOnConvergedExit(t *testing.T) {
	e := exitFixture(t, false)
	var probed []string
	tieTunnelToCredentials(e, &probed)
	if id := e.start(t, KindProvision); e.store.jobs[id].Status != JobSucceeded {
		t.Fatalf("provision: %s %v", e.store.jobs[id].Error, e.store.statuses(id))
	}
	k := e.keys(t)
	e.node.files[config.ExitKeysPath] = []byte(legacyKeysJSON(k))
	probed = nil
	writes, hubWrites, restarts, hubRestarts, tunnel, syncs := e.node.writes, e.hub.writes, maps.Clone(e.node.restarts), maps.Clone(e.hub.restarts), e.h.tunnel, e.h.syncs

	id := e.start(t, KindVerify)
	if got := e.store.statuses(id); !slices.Equal(got, withStatus(exitSteps, "ok", "ok", "drift", "ok", "skipped", "ok", "ok", "ok", "ok", "ok", "ok", "ok", "ok", "ok", "ok")) {
		t.Fatalf("verify: %v\n%s", got, e.tail(id, "reality_keys"))
	}
	if tl := e.tail(id, "reality_keys"); !strings.Contains(tl, legacyKeysDrift) || strings.Contains(tl, "некорректен") {
		t.Fatalf("reality_keys: %q", tl)
	}
	if e.node.writes != writes || !maps.Equal(e.node.restarts, restarts) || e.h.syncs != syncs || len(probed) != 1 || probed[0] != stlsPW(t, k.UUID) {
		t.Fatalf("verify touched the node (writes %d) or probed with another password (%d probes)", e.node.writes-writes, len(probed))
	}

	id = e.start(t, KindReprovision)
	if got := e.store.statuses(id); e.store.jobs[id].Status != JobSucceeded ||
		!slices.Equal(got, withStatus(exitSteps, "ok", "ok", "done", "ok", "done", "ok", "ok", "ok", "ok", "ok", "ok", "ok", "ok", "ok", "ok")) {
		t.Fatalf("converge: %s %v", e.store.jobs[id].Error, got)
	}
	if e.keys(t) != k || e.node.writes != writes+1 || e.hub.writes != hubWrites || !maps.Equal(e.node.restarts, restarts) ||
		!maps.Equal(e.hub.restarts, hubRestarts) || e.h.tunnel != tunnel || e.h.syncs != syncs {
		t.Fatalf("only the keys file may change: keys kept %v, writes %d/%d, restarts %v %v, tunnel kept %v, syncs %d",
			e.keys(t) == k, e.node.writes-writes, e.hub.writes-hubWrites, e.node.restarts, e.hub.restarts, e.h.tunnel == tunnel, e.h.syncs-syncs)
	}
	for _, st := range e.store.jobs[id].Steps {
		noSecrets(t, []string{st.OutputTail}, k.UUID, k.SSPassword, k.PrivateKey)
	}
}

// An online exit whose keys file was deleted: verify reports that new keys would cut its tunnel,
// «Переустановить» stops at the keys — nothing is written, no service restarts, the database row
// and the configs keep the running credentials (before the fix it regenerated all of them).
func TestExitDeletedKeysFileNeverRegenerated(t *testing.T) {
	e := exitFixture(t, false)
	var probed []string
	tieTunnelToCredentials(e, &probed)
	if id := e.start(t, KindProvision); e.store.jobs[id].Status != JobSucceeded {
		t.Fatalf("provision: %s %v", e.store.jobs[id].Error, e.store.statuses(id))
	}
	k := e.keys(t)
	e.node.remove(config.ExitKeysPath)
	writes, restarts, hubRestarts, tunnel, syncs := e.node.writes, maps.Clone(e.node.restarts), maps.Clone(e.hub.restarts), e.h.tunnel, e.h.syncs
	yaml, xc := string(e.node.files[config.ExitHysteriaConfig]), string(e.node.files[config.ExitSTLSConfig])

	id := e.start(t, KindVerify)
	if st := e.steps(id); st["reality_keys"] != StepDrift || !strings.Contains(e.tail(id, "reality_keys"), "новые ключи разорвали бы его, не создаю") {
		t.Fatalf("verify: %v %q", st, e.tail(id, "reality_keys"))
	}
	id = e.start(t, KindReprovision)
	j := e.store.jobs[id]
	if j.Status != JobFailed || e.steps(id)["reality_keys"] != StepFailed || !strings.Contains(j.Error, "туннель у этого выхода уже есть") {
		t.Fatalf("%s %q %v", j.Status, j.Error, e.store.statuses(id))
	}
	if e.node.writes != writes || e.node.files[config.ExitKeysPath] != nil || string(e.node.files[config.ExitHysteriaConfig]) != yaml ||
		string(e.node.files[config.ExitSTLSConfig]) != xc || xc == "" || !maps.Equal(e.node.restarts, restarts) || !maps.Equal(e.hub.restarts, hubRestarts) ||
		e.h.tunnel != tunnel || e.h.syncs != syncs || e.h.lifecycle != "online" {
		t.Fatalf("the refused converge changed something: writes %d, keys file %v", e.node.writes-writes, e.node.files[config.ExitKeysPath] != nil)
	}
	noSecrets(t, append([]string{j.Error, e.tail(id, "reality_keys")}, e.logs...), k.UUID, k.SSPassword, k.PrivateKey)
}

// exitTwoOldKeys is exit 2 as the verify of 2026-10-26 found it: legacyExit's old deploy, with
// the keys file of the old format (root-only) and the Hub's UUID only as the client of the
// Reality inbound in its xray config (the Shadowsocks inbounds' clients carry no id).
func exitTwoOldKeys(t *testing.T) (*exitEnv, config.ExitRealityKeys) {
	t.Helper()
	e, k := legacyExit(t)
	n := e.node
	n.files[config.ExitKeysPath], n.modes[config.ExitKeysPath] = []byte(legacyKeysJSON(k)), 0o600
	n.files[config.ExitXrayConfig] = []byte(fmt.Sprintf(`{"inbounds": [
  {"tag": "shadowsocks-ws", "port": 443, "protocol": "shadowsocks", "settings": {"clients": [{"method": "chacha20-ietf-poly1305", "password": "c3MtcGFzc3dvcmQtb2xk"}]}},
  {"tag": "shadowsocks-tcp", "port": 8388, "protocol": "shadowsocks", "settings": {"method": "chacha20-ietf-poly1305", "password": "c3MtcGFzc3dvcmQtb2xk"}},
  {"tag": "vless-reality", "port": 10443, "protocol": "vless", "settings": {"clients": [{"id": %q, "flow": "xtls-rprx-vision"}], "decryption": "none"}}
]}`, k.UUID))
	return e, k
}

// Exit 2's converge with the old keys file: verify reports the keys as drift that keeps the
// values, the later steps show exactly the drift they show with a full keys file (the templates,
// the stale database row) and both paths answer with the adopted credentials; the converge keeps
// the tunnel's UUID and password everywhere — the file, the configs, the database, the Hub's
// client — with each path checked around its change. A database that disagrees with the exit
// stops the converge at the keys, before anything is written.
func TestExitTwoConvergesWithOldKeysFile(t *testing.T) {
	t.Run("database disagrees", func(t *testing.T) {
		e, k := exitTwoOldKeys(t)
		o := otherKeys(t, k)
		e.h.tunnel.Password = o.SSPassword
		writes, yaml, tunnel := e.node.writes, string(e.node.files[config.ExitHysteriaConfig]), e.h.tunnel
		id := e.start(t, KindReprovision)
		j := e.store.jobs[id]
		if j.Status != JobFailed || e.steps(id)["reality_keys"] != StepFailed || !strings.Contains(j.Error, "не совпадает") ||
			e.node.writes != writes || string(e.node.files[config.ExitHysteriaConfig]) != yaml || e.h.tunnel != tunnel || e.h.lifecycle != "online" {
			t.Fatalf("%s %q %v (writes %d)", j.Status, j.Error, e.store.statuses(id), e.node.writes-writes)
		}
		noSecrets(t, append([]string{j.Error, e.tail(id, "reality_keys")}, e.logs...), k.UUID, k.SSPassword, o.SSPassword, k.PrivateKey)
	})

	e, k := exitTwoOldKeys(t)
	var probed []string
	tieTunnelToCredentials(e, &probed)
	writes := e.node.writes
	id := e.start(t, KindVerify)
	// The old exit has no ShadowTLS yet: its tunnel and the Hub's client drift, the backup path is
	// down and the retired xray still there.
	if got := e.store.statuses(id); !slices.Equal(got, withStatus(exitSteps, "ok", "drift", "drift", "ok", "skipped", "drift", "drift", "drift", "drift", "drift", "drift", "ok", "drift", "drift", "ok")) {
		t.Fatalf("verify: %v", got)
	}
	if tl := e.tail(id, "reality_keys"); !strings.Contains(tl, legacyKeysDrift) || strings.Contains(tl, "некорректен") {
		t.Fatalf("reality_keys: %q", tl)
	}
	for _, step := range []string{"hysteria", "stls", "save_tunnel", "hub_client", "hub_stls", "path_tcp"} {
		if tl := e.tail(id, step); strings.Contains(tl, "ключи туннеля не прочитаны") {
			t.Fatalf("%s did not compare against the adopted keys: %q", step, tl)
		}
	}
	if e.node.writes != writes || len(probed) != 0 {
		t.Fatalf("verify wrote %d files or probed a backup client that is not there (%d probes)", e.node.writes-writes, len(probed))
	}

	id = e.start(t, KindReprovision)
	if got := e.store.statuses(id); e.store.jobs[id].Status != JobSucceeded ||
		!slices.Equal(got, withStatus(exitSteps, "ok", "done", "done", "ok", "done", "done", "done", "done", "done", "done", "done", "ok", "ok", "done", "ok")) {
		t.Fatalf("converge: %s %v", e.store.jobs[id].Error, got)
	}
	if e.keys(t) != k || e.h.tunnel != dbTunnel(k) {
		t.Fatalf("the tunnel credentials must survive: file %v, database %v", e.keys(t) == k, e.h.tunnel == dbTunnel(k))
	}
	// Both ends of the backup tunnel derive from the adopted UUID, and the backup path answered with them.
	if pw, _ := hysteriaAuthPassword(string(e.node.files[config.ExitHysteriaConfig])); pw != k.SSPassword ||
		!strings.Contains(string(e.node.files[config.ExitSTLSConfig]), `"password": "`+stlsPW(t, k.UUID)+`"`) ||
		!strings.Contains(string(e.hub.files[config.HubSTLSConfigPath(7)]), `"password": "`+stlsPW(t, k.UUID)+`"`) ||
		!strings.Contains(string(e.hub.files[config.HubClientConfigPath(7)]), "\nauth: "+k.SSPassword+"\n") ||
		len(probed) == 0 || probed[len(probed)-1] != stlsPW(t, k.UUID) || e.node.files[config.ExitXrayConfig] != nil {
		t.Fatal("hysteria, the backup tunnel and the Hub's clients must keep the adopted credentials; the old xray must be gone")
	}
	for _, st := range e.store.jobs[id].Steps {
		noSecrets(t, []string{st.OutputTail}, k.UUID, k.SSPassword, k.PrivateKey)
	}
}

// legacyXrayConfigJSON is the exit xray config of the retired Reality path (the shape the old
// renderer wrote until 2026-11-16): the inbound vless-reality on :10443 with the tunnel UUID as
// its one client — what liveRealityClients still reads to adopt a legacy exit's keys.
func legacyXrayConfigJSON(k config.ExitRealityKeys, _ string, _ int) ([]byte, error) {
	return json.MarshalIndent(map[string]any{"inbounds": []any{map[string]any{
		"tag": "vless-reality", "listen": "0.0.0.0", "port": config.ExitRealityPort, "protocol": "vless",
		"settings": map[string]any{"decryption": "none", "clients": []any{map[string]any{"id": k.UUID, "flow": "xtls-rprx-vision"}}},
	}}}, "", "  ")
}
