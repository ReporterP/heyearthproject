package provision

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/heyearth/backend/internal/config"
)

// The tunnel keys live on the exit (config.ExitKeysPath, 0600): the Reality key pair, the short
// id, the VLESS UUID and the hysteria password. The database keeps the public parts and the
// password (save_tunnel), never the private key. Missing parts are generated here, in Go
// (X25519 through crypto/ecdh — no parsing of `xray x25519`, whose output format changed) —
// except the UUID and password of a keys file of the old format, which are adopted from what the
// tunnel already runs with (adoptLegacyKeys), never generated anew.

var (
	uuidRe     = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	shortIDRe  = regexp.MustCompile(`^([0-9a-f]{2}){1,8}$`)
	tunnelPwRe = regexp.MustCompile(`^[A-Za-z0-9+/=]{16,}$`)
)

// errKeysCorrupt: the keys file is there but is not JSON — never overwritten.
var errKeysCorrupt = errors.New("файл ключей " + config.ExitKeysPath + " не читается как JSON")

// readKeys reads the keys file; ok false when it is absent or empty. It is all secrets and none
// of them is known to the run's redaction before it is parsed, so it goes through readSecretFile.
func readKeys(ctx context.Context, r Remote) (config.ExitRealityKeys, bool, error) {
	var k config.ExitRealityKeys
	out, err := readSecretFile(ctx, r, config.ExitKeysPath, "файл ключей туннеля")
	if err != nil || strings.TrimSpace(out) == "" {
		return k, false, err
	}
	if err := json.Unmarshal([]byte(out), &k); err != nil {
		return config.ExitRealityKeys{}, true, errKeysCorrupt // json's error quotes the content
	}
	return k, true, nil
}

// secretReadKinds are the error kinds a failed read keeps (the runner's inconclusive, the
// bestEffort branches of deprovision); nothing else of the failed Run survives.
var secretReadKinds = []error{context.Canceled, context.DeadlineExceeded, ErrNodeUnreachable, ErrHostKeyNotConfirmed,
	ErrNoCredentials, ErrHostKeyMismatch, ErrConnectionLost}

// readSecretFile prints a file whose content must never reach an error, a step's output or the
// log (config.CatScript; "" when absent). A failed Run's error is dropped, not wrapped: the SSH
// remote quotes the output tail in it — an SSH drop right after `cat` would carry the whole file.
// The returned error names the file and keeps only the kinds of secretReadKinds.
func readSecretFile(ctx context.Context, r Remote, path, what string) (string, error) {
	out, err := r.Run(ctx, config.CatScript(path))
	if err == nil {
		return out, nil
	}
	format, args := "", []any{}
	for _, kind := range secretReadKinds {
		if errors.Is(err, kind) {
			format += "%w: "
			args = append(args, kind)
		}
	}
	if len(args) == 0 {
		format += "команда на ноде завершилась с ошибкой: "
	}
	return "", fmt.Errorf(format+"%s %s не прочитан (вывод не показан: в нём секреты)", append(args, what, path)...)
}

// keysVerify: the file is there with valid keys and is readable by root only. Valid keys are
// taken into the run even when only the mode is wrong, so the later steps (and a verify job)
// compare against the node's real keys. A file of the old format is drift, not a broken file: the
// run takes the values keysApply would adopt (adoptLegacyKeys), so the later steps compare
// against them and show only what really differs.
func keysVerify(ctx context.Context, r Remote, s *State) error {
	k, ok, err := readKeys(ctx, r)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("файла ключей туннеля ещё нет")
	}
	k, adopted, err := adoptLegacyKeys(ctx, r, s, k)
	if err != nil {
		return err
	}
	if adopted != nil && (k.UUID == "" || k.SSPassword == "") {
		return errors.New("старый формат файла ключей: " + strings.Join(adopted, "; "))
	}
	if err := validKeys(k); err != nil {
		return err
	}
	s.Keys = k
	var drift []string
	if adopted != nil {
		drift = append(drift, legacyKeysDrift+" ("+strings.Join(adopted, "; ")+")")
	}
	states, err := remoteStates(ctx, r, []File{{Path: config.ExitKeysPath}})
	if err != nil {
		return err
	}
	if st, ok := states[config.ExitKeysPath]; !ok || !privateMode(st.mode) {
		drift = append(drift, fmt.Sprintf("файл ключей %s доступен не только root (права %s)", config.ExitKeysPath, orNone(st.mode)))
	}
	if len(drift) > 0 {
		return errors.New(strings.Join(drift, "; "))
	}
	return nil
}

// keysApply keeps every existing key (exit 2 keeps its tunnel) and only adds missing ones; a file
// that cannot be read is never overwritten. A file of the old format gets the tunnel's running
// UUID and password (adoptLegacyKeys) and is rewritten in the full format with the same values —
// atomically (PutFile), root-only.
func keysApply(ctx context.Context, r Remote, s *State) error {
	k, _, err := readKeys(ctx, r)
	switch {
	case errors.Is(err, errKeysCorrupt):
		return fmt.Errorf("%w — не перезаписываю, нужна ручная проверка", err)
	case err != nil:
		return err
	}
	k, adopted, err := adoptLegacyKeys(ctx, r, s, k)
	if err != nil {
		return err
	}
	if k, err = completeKeys(k); err != nil {
		return err
	}
	b, err := json.MarshalIndent(k, "", "  ")
	if err != nil {
		return err
	}
	f := File{config.ExitKeysPath, append(b, '\n'), 0o600}
	states, err := remoteStates(ctx, r, []File{f})
	if err != nil {
		return err
	}
	if err := prepareWrites(ctx, r, s, "", []File{f}, states); err != nil {
		return err
	}
	if _, err := r.PutFile(ctx, f.Path, f.Data, f.Mode); err != nil {
		return err
	}
	s.Keys = k
	if adopted != nil {
		s.Printf("файл ключей старого формата переписан в полный: %s", strings.Join(adopted, "; "))
	}
	s.Printf("ключи туннеля на месте (открытый ключ %s…)", k.PublicKey[:8])
	return nil
}

// ── a keys file of the old format: adopt, never rotate ──

// legacyKeysDrift: a verify job's word for a keys file of the old format (adoptLegacyKeys).
const legacyKeysDrift = "старый формат файла ключей — значения сохранятся"

// legacyHysteriaConfig is where the Hub keeps its hysteria server config. An exit's server runs
// with config.ExitHysteriaConfig; a copy here is read only to cross-check a password being adopted
// — every copy on the exit must carry the same one.
const legacyHysteriaConfig = "/etc/hysteria/server.yaml"

// legacyKeys: the old deploy's keys file — the Reality key pair and short id without the tunnel
// UUID and/or password (exit 2, measured 2026-10-26: those two live only in its xray and hysteria
// configs and in servers.tunnel_uuid / tunnel_ss_password).
func legacyKeys(k config.ExitRealityKeys) bool {
	return k.PrivateKey != "" && (k.UUID == "" || k.SSPassword == "")
}

// liveValues: where on the exit a tunnel credential was found and the values it carries there.
type liveValues struct {
	where string
	vals  []string
}

// adoptLegacyKeys completes a keys file of the old format with the values the tunnel already runs
// with, never with new ones: a new UUID or password would cut the Hub off the exit until every
// side is rewritten. Each missing value comes from the database (what the Hub dials with),
// cross-checked against the exit's live configs where they carry it — the Reality inbound's
// clients of the xray config, the auth password of every hysteria server config; without a
// database value the one value the live configs agree on is taken. A disagreement or a malformed
// value refuses: nothing is chosen, nothing generated. A value found nowhere stays missing
// (completeKeys creates it: there is no tunnel to keep). adopted says, per credential, where its
// value came from (nil: the file is not of the old format); values never reach it, an error or a
// step's output, and every value read is registered as a secret of the run.
func adoptLegacyKeys(ctx context.Context, r Remote, s *State, k config.ExitRealityKeys) (_ config.ExitRealityKeys, adopted []string, _ error) {
	if !legacyKeys(k) {
		return k, nil, nil
	}
	var db Tunnel
	if s.Hooks.LoadTunnel != nil {
		t, err := s.Hooks.LoadTunnel(ctx, s.Node.ID)
		if err != nil {
			return k, nil, fmt.Errorf("старый формат файла ключей, а данные туннеля в базе не прочитаны — ничего не меняю: %w", err)
		}
		db = t
	}
	s.AddSecret(db.UUID, db.Password)
	adopt := func(what string, valid *regexp.Regexp, db string, read func(context.Context, Remote) ([]liveValues, error)) (string, error) {
		live, err := read(ctx, r)
		if err != nil {
			return "", err
		}
		for _, l := range live {
			s.AddSecret(l.vals...)
		}
		v, note, err := adoptValue(what, valid, db, live)
		if err != nil {
			return "", err
		}
		adopted = append(adopted, note)
		return v, nil
	}
	var err error
	if k.UUID == "" {
		if k.UUID, err = adopt("UUID туннеля", uuidRe, db.UUID, liveRealityClients); err != nil {
			return k, nil, err
		}
	}
	if k.SSPassword == "" {
		if k.SSPassword, err = adopt("пароль туннеля", tunnelPwRe, db.Password, liveHysteriaPasswords); err != nil {
			return k, nil, err
		}
	}
	return k, adopted, nil
}

// adoptValue picks one credential missing from the keys file: the database's value when it has
// one (every live copy must carry it), else the one value the live copies agree on; "" when there
// is none anywhere. note says where it came from, without the value.
func adoptValue(what string, valid *regexp.Regexp, db string, live []liveValues) (v, note string, err error) {
	var where []string
	for _, l := range live {
		where = append(where, l.where)
	}
	refuse := func(format string, a ...any) error {
		return fmt.Errorf("старый формат файла ключей: "+format+" — не перенимаю и новый не создаю, нужна ручная проверка", a...)
	}
	if db != "" {
		for _, l := range live {
			if !slices.Contains(l.vals, db) {
				return "", "", refuse("%s в базе не совпадает с %s на ноде", what, l.where)
			}
		}
		switch {
		case !valid.MatchString(db):
			return "", "", refuse("%s в базе некорректен", what)
		case len(live) == 0:
			return db, what + " — из базы (на ноде не найден, сверить не с чем)", nil
		}
		return db, what + " — из базы, совпадает с " + strings.Join(where, ", "), nil
	}
	var seen []string
	for _, l := range live {
		for _, x := range l.vals {
			if !slices.Contains(seen, x) {
				seen = append(seen, x)
			}
		}
	}
	switch {
	case len(seen) == 0:
		return "", what + " — нет ни в базе, ни на ноде: будет создан новый", nil
	case len(seen) > 1:
		return "", "", refuse("%s в базе нет, а на ноде разные значения (%s)", what, strings.Join(where, ", "))
	case !valid.MatchString(seen[0]):
		return "", "", refuse("%s в базе нет, а на ноде (%s) он некорректен", what, strings.Join(where, ", "))
	}
	return seen[0], what + " — с ноды (" + strings.Join(where, ", ") + "), в базе его нет", nil
}

// liveRealityClients: the client ids of the exit's Reality inbound (TCP 10443 or tag
// vless-reality of config.ExitXrayConfig); none when the file is absent or not readable as an
// xray config. The file carries the private key: read through readSecretFile.
func liveRealityClients(ctx context.Context, r Remote) ([]liveValues, error) {
	out, err := readSecretFile(ctx, r, config.ExitXrayConfig, "конфиг xray")
	if err != nil || strings.TrimSpace(out) == "" {
		return nil, err
	}
	var cfg struct {
		Inbounds []struct {
			Tag      string          `json:"tag"`
			Port     json.RawMessage `json:"port"`
			Settings struct {
				Clients []struct {
					ID string `json:"id"`
				} `json:"clients"`
			} `json:"settings"`
		} `json:"inbounds"`
	}
	if json.Unmarshal([]byte(out), &cfg) != nil { // never passed on: its error may quote the content
		return nil, nil
	}
	var ids []string
	for _, in := range cfg.Inbounds {
		if in.Tag != "vless-reality" && strings.Trim(string(in.Port), `"`) != strconv.Itoa(config.ExitRealityPort) {
			continue
		}
		for _, c := range in.Settings.Clients {
			if c.ID != "" {
				ids = append(ids, c.ID)
			}
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return []liveValues{{config.ExitXrayConfig + " (клиент Reality)", ids}}, nil
}

// liveHysteriaPasswords: the auth password of every hysteria server config on the exit that has
// one (config.ExitHysteriaConfig, legacyHysteriaConfig).
func liveHysteriaPasswords(ctx context.Context, r Remote) ([]liveValues, error) {
	var live []liveValues
	for _, p := range []string{config.ExitHysteriaConfig, legacyHysteriaConfig} {
		out, err := readSecretFile(ctx, r, p, "конфиг hysteria")
		if err != nil {
			return nil, err
		}
		if pw, ok := hysteriaAuthPassword(out); ok {
			live = append(live, liveValues{p, []string{pw}})
		}
	}
	return live, nil
}

// hysteriaAuthPassword: the password of a hysteria server config's top-level `auth:` block of
// type password — as config.ExitHysteriaServerYAML renders it and as hand-written configs have it
// (plain, double- or single-quoted, comments); ok false when there is none.
func hysteriaAuthPassword(y string) (string, bool) {
	inAuth, indent, typ, pw, have := false, "", "", "", false
	for _, line := range strings.Split(y, "\n") {
		line = strings.TrimRight(line, "\r")
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		lead := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		if lead == "" {
			key, _, _ := strings.Cut(t, ":")
			inAuth, indent = key == "auth", ""
			continue
		}
		if !inAuth {
			continue
		}
		if indent == "" {
			indent = lead
		}
		key, val, ok := strings.Cut(t, ":")
		if lead != indent || !ok { // a deeper level: not one of the block's own keys
			continue
		}
		switch key {
		case "type":
			typ = yamlScalar(val)
		case "password":
			pw, have = yamlScalar(val), true
		}
	}
	if !have || pw == "" || typ != "password" {
		return "", false
	}
	return pw, true
}

// yamlScalar: the value of a one-line YAML scalar — double-quoted (Go escapes, as config's
// yamlPassword renders), single-quoted (a doubled quote stands for one) or plain (" #" starts a
// comment); "" when a quoted one is malformed.
func yamlScalar(v string) string {
	v = strings.TrimSpace(v)
	switch {
	case strings.HasPrefix(v, `"`):
		if j := strings.LastIndex(v, `"`); j > 0 {
			if u, err := strconv.Unquote(v[:j+1]); err == nil {
				return u
			}
		}
		return ""
	case strings.HasPrefix(v, "'"):
		if j := strings.LastIndex(v, "'"); j > 0 {
			return strings.ReplaceAll(v[1:j], "''", "'")
		}
		return ""
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	return v
}

func derivePublic(priv string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(priv)
	if err != nil || len(raw) != 32 {
		return "", errors.New("закрытый ключ Reality повреждён")
	}
	k, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return "", errors.New("закрытый ключ Reality повреждён")
	}
	return base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()), nil
}

// completeKeys generates what is missing and derives the public key from the private one.
func completeKeys(k config.ExitRealityKeys) (config.ExitRealityKeys, error) {
	if k.PrivateKey == "" {
		priv, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			return k, err
		}
		k.PrivateKey = base64.RawURLEncoding.EncodeToString(priv.Bytes())
	}
	pub, err := derivePublic(k.PrivateKey)
	if err != nil {
		return k, err
	}
	k.PublicKey = pub
	if k.ShortID == "" {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			return k, err
		}
		k.ShortID = hex.EncodeToString(b)
	}
	if k.UUID == "" {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return k, err
		}
		b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
		k.UUID = fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
	}
	if k.SSPassword == "" {
		if k.SSPassword, err = randomPassword(); err != nil {
			return k, err
		}
	}
	return k, validKeys(k)
}

func validKeys(k config.ExitRealityKeys) error {
	pub, err := derivePublic(k.PrivateKey)
	switch {
	case err != nil:
		return err
	case pub != k.PublicKey:
		return errors.New("открытый ключ Reality не соответствует закрытому")
	case !uuidRe.MatchString(k.UUID):
		return errors.New("UUID туннеля некорректен")
	case !shortIDRe.MatchString(k.ShortID):
		return errors.New("shortId некорректен")
	case !tunnelPwRe.MatchString(k.SSPassword):
		return errors.New("пароль туннеля некорректен")
	}
	return nil
}
