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
// against them and show only what really differs. Without the Reality key pair (no file) it says
// whether keysApply could create new keys (refuseNewKeys).
func keysVerify(ctx context.Context, r Remote, s *State) error {
	k, ok, err := readKeys(ctx, r)
	if err != nil {
		return err
	}
	if k.PrivateKey == "" {
		if err := refuseNewKeys(ctx, r, s, ok); err != nil {
			return err
		}
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
// atomically (PutFile), root-only. New keys are created only for an exit that has no tunnel
// anywhere (refuseNewKeys).
func keysApply(ctx context.Context, r Remote, s *State) error {
	k, ok, err := readKeys(ctx, r)
	switch {
	case errors.Is(err, errKeysCorrupt):
		return fmt.Errorf("%w — не перезаписываю, нужна ручная проверка", err)
	case err != nil:
		return err
	}
	if k.PrivateKey == "" {
		if err := refuseNewKeys(ctx, r, s, ok); err != nil {
			return err
		}
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

// ── credentials the keys file lacks: adopt or refuse, never rotate ──

// legacyKeysDrift: a verify job's word for a keys file of the old format (adoptLegacyKeys).
const legacyKeysDrift = "старый формат файла ключей — значения сохранятся"

// tunnelCred is one tunnel credential of the keys file: its database column (what the Hub dials
// with) and its live copies on the exit.
type tunnelCred struct {
	what, column string
	valid        *regexp.Regexp
	live         func(ctx context.Context, r Remote) ([]liveValues, error)
}

const (
	whatUUID     = "UUID туннеля"
	whatPassword = "пароль туннеля"
)

var (
	credUUID     = tunnelCred{whatUUID, "servers.tunnel_uuid", uuidRe, liveRealityClients}
	credPassword = tunnelCred{whatPassword, "servers.tunnel_ss_password", tunnelPwRe, liveHysteriaPasswords}
)

// liveValues: one live copy of a credential on the exit — where names the exact place — and the
// values it carries.
type liveValues struct {
	where string
	vals  []string
}

// unparsedConfig: a live config is there (not empty) but its credential cannot be read from it.
// It never carries the content.
type unparsedConfig struct{ path, what string }

func (e *unparsedConfig) Error() string {
	return fmt.Sprintf("%s на ноде не разобран (содержимое не показано) — %s по нему не сверить", e.path, e.what)
}

// legacyKeys: the old deploy's keys file — the Reality key pair and short id without the tunnel
// UUID and/or password (exit 2, measured 2026-10-26: those two live only in its xray and hysteria
// configs and in servers.tunnel_uuid / tunnel_ss_password).
func legacyKeys(k config.ExitRealityKeys) bool {
	return k.PrivateKey != "" && (k.UUID == "" || k.SSPassword == "")
}

// loadDBTunnel: the node's servers.tunnel_* (zero without the hook), its credentials registered
// as secrets of the run.
func loadDBTunnel(ctx context.Context, s *State) (Tunnel, error) {
	if s.Hooks.LoadTunnel == nil {
		return Tunnel{}, nil
	}
	t, err := s.Hooks.LoadTunnel(ctx, s.Node.ID)
	if err != nil {
		return Tunnel{}, err
	}
	s.AddSecret(t.UUID, t.Password)
	return t, nil
}

// refuseNewKeys guards the creation of new Reality keys — the keys file is absent or has no key
// pair (fileThere: which of the two): new keys are created only when this exit has no tunnel
// anywhere, neither in its database row (servers.tunnel_uuid / tunnel_ss_password) nor in a live
// config (the Reality client of the xray config, the hysteria auth password; a config that cannot
// be read counts too — it may well carry one). Otherwise new keys would cut a tunnel the Hub
// dials: refused, nothing written. Values never reach the error.
func refuseNewKeys(ctx context.Context, r Remote, s *State, fileThere bool) error {
	db, err := loadDBTunnel(ctx, s)
	if err != nil {
		return fmt.Errorf("ключей туннеля на ноде нет, а данные туннеля в базе не прочитаны — ничего не создаю: %w", err)
	}
	var inDB, onNode []string
	if db.UUID != "" {
		inDB = append(inDB, credUUID.column)
	}
	if db.Password != "" {
		inDB = append(inDB, credPassword.column)
	}
	for _, c := range []tunnelCred{credUUID, credPassword} {
		live, err := c.live(ctx, r)
		var bad *unparsedConfig
		switch {
		case errors.As(err, &bad):
			onNode = append(onNode, bad.path+" (не разобран)")
		case err != nil:
			return err
		}
		for _, l := range live {
			s.AddSecret(l.vals...)
			onNode = append(onNode, l.where)
		}
	}
	if len(inDB)+len(onNode) == 0 {
		return nil
	}
	var found []string
	if len(inDB) > 0 {
		found = append(found, "в базе "+strings.Join(inDB, ", "))
	}
	if len(onNode) > 0 {
		found = append(found, "на ноде "+strings.Join(onNode, ", "))
	}
	what := "файла ключей туннеля " + config.ExitKeysPath + " нет"
	if fileThere {
		what = "в файле ключей туннеля " + config.ExitKeysPath + " нет ключей Reality"
	}
	return fmt.Errorf("%s, а туннель у этого выхода уже есть (%s) — новые ключи разорвали бы его, не создаю. "+
		"Верните файл ключей из резервной копии; если сервер переустановлен и старый туннель не нужен — удалите ноду и добавьте её заново",
		what, strings.Join(found, "; "))
}

// adoptLegacyKeys completes a keys file of the old format with the values the tunnel already runs
// with, never with new ones: a new UUID or password would cut the Hub off the exit until every
// side is rewritten. Each missing value comes from the database (what the Hub dials with),
// cross-checked against every live copy on the exit (tunnelCred.live); without a database value
// the one value the live copies agree on is taken. A disagreement, a malformed value or a live
// config that cannot be read refuses: nothing is chosen, nothing generated. A value found nowhere
// stays missing (completeKeys creates it: there is no tunnel to keep). adopted says, per
// credential, where its value came from (nil: the file is not of the old format); values never
// reach it, an error or a step's output, and every value read is registered as a secret of the run.
func adoptLegacyKeys(ctx context.Context, r Remote, s *State, k config.ExitRealityKeys) (_ config.ExitRealityKeys, adopted []string, _ error) {
	if !legacyKeys(k) {
		return k, nil, nil
	}
	db, err := loadDBTunnel(ctx, s)
	if err != nil {
		return k, nil, fmt.Errorf("старый формат файла ключей, а данные туннеля в базе не прочитаны — ничего не меняю: %w", err)
	}
	for _, c := range []struct {
		cred tunnelCred
		dst  *string
		db   string
	}{{credUUID, &k.UUID, db.UUID}, {credPassword, &k.SSPassword, db.Password}} {
		if *c.dst != "" {
			continue
		}
		v, note, err := c.cred.adopt(ctx, r, s, c.db)
		if err != nil {
			return k, nil, err
		}
		*c.dst, adopted = v, append(adopted, note)
	}
	return k, adopted, nil
}

// adopt picks the credential's value: the database's when it has one (every live copy must carry
// it), else the one value the live copies agree on; "" when there is none anywhere. note says
// where it came from, without the value.
func (c tunnelCred) adopt(ctx context.Context, r Remote, s *State, db string) (v, note string, err error) {
	refuse := func(format string, a ...any) error {
		return fmt.Errorf("старый формат файла ключей: "+format+" — не перенимаю и новый не создаю, нужна ручная проверка", a...)
	}
	live, err := c.live(ctx, r)
	var bad *unparsedConfig
	switch {
	case errors.As(err, &bad):
		return "", "", refuse("%v", err)
	case err != nil:
		return "", "", err
	}
	var where, seen []string
	for _, l := range live {
		s.AddSecret(l.vals...)
		where = append(where, l.where)
		for _, x := range l.vals {
			if !slices.Contains(seen, x) {
				seen = append(seen, x)
			}
		}
	}
	inDB := c.what + " в базе (" + c.column + ")"
	if db != "" {
		for _, l := range live {
			if !slices.Contains(l.vals, db) {
				return "", "", refuse("%s не совпадает с %s", inDB, l.where)
			}
		}
		switch {
		case !c.valid.MatchString(db):
			return "", "", refuse("%s некорректен", inDB)
		case len(live) == 0:
			return db, c.what + " — из базы (" + c.column + "); на ноде его копии нет, сверить не с чем", nil
		}
		return db, c.what + " — из базы (" + c.column + "), совпадает с " + strings.Join(where, ", "), nil
	}
	switch {
	case len(seen) == 0:
		return "", c.what + " — нет ни в базе (" + c.column + "), ни на ноде: будет создан новый", nil
	case len(seen) > 1:
		return "", "", refuse("%s пуст, а на ноде разные значения: %s", inDB, strings.Join(where, ", "))
	case !c.valid.MatchString(seen[0]):
		return "", "", refuse("%s пуст, а значение на ноде (%s) некорректно", inDB, strings.Join(where, ", "))
	}
	return seen[0], c.what + " — с ноды (" + strings.Join(where, ", ") + "); в базе (" + c.column + ") пусто", nil
}

// realityClientWhere: the place of the tunnel UUID in the exit's xray config, as the texts name it.
var realityClientWhere = "id клиента Reality (inbound :" + strconv.Itoa(config.ExitRealityPort) + ") в " + config.ExitXrayConfig

// liveRealityClients: the client ids of the exit's Reality inbound (TCP 10443 or tag
// vless-reality of config.ExitXrayConfig, comments allowed as xray allows them); none when the
// file is absent or empty or no such inbound has clients; *unparsedConfig when it is not JSON of
// that shape. The file carries the private key: read through readSecretFile.
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
	if json.Unmarshal(stripJSONComments(out), &cfg) != nil { // never passed on: its error may quote the content
		return nil, &unparsedConfig{config.ExitXrayConfig, whatUUID}
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
	return []liveValues{{realityClientWhere, ids}}, nil
}

// stripJSONComments removes // and /* */ comments outside strings (xray reads its config so).
func stripJSONComments(s string) []byte {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
			j := i + 1
			for ; j < len(s) && s[j] != '"'; j++ {
				if s[j] == '\\' {
					j++
				}
			}
			if j >= len(s) {
				j = len(s) - 1
			}
			out = append(out, s[i:j+1]...)
			i = j
		case c == '/' && i+1 < len(s) && s[i+1] == '/':
			for i < len(s) && s[i] != '\n' {
				i++
			}
			if i < len(s) {
				out = append(out, '\n')
			}
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return append(out, "/*"...) // unterminated: left for json to refuse
			}
			out = append(out, ' ')
			i += end + 3
		default:
			out = append(out, c)
		}
	}
	return out
}

// liveHysteriaPasswords: the auth password of every hysteria server config on the exit
// (config.ExitHysteriaConfig, config.ExitHysteriaConfigLegacy) that is there and not empty;
// *unparsedConfig for one whose password cannot be read (hysteriaAuthPassword).
func liveHysteriaPasswords(ctx context.Context, r Remote) ([]liveValues, error) {
	var live []liveValues
	for _, p := range []string{config.ExitHysteriaConfig, config.ExitHysteriaConfigLegacy} {
		out, err := readSecretFile(ctx, r, p, "конфиг hysteria")
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(out) == "" {
			continue
		}
		pw, ok := hysteriaAuthPassword(out)
		if !ok {
			return nil, &unparsedConfig{p, whatPassword}
		}
		live = append(live, liveValues{"«password:» блока auth в " + p, []string{pw}})
	}
	return live, nil
}

// hysteriaAuthPassword: the password of a hysteria server config's top-level `auth:` of type
// password — a block (as config.ExitHysteriaServerYAML renders it) or a flow map
// (`auth: {type: password, password: …}`), scalars plain, double- or single-quoted, comments
// anywhere YAML allows them. ok false when there is no such password or the config has a form
// this parser does not know (another auth type included): the caller then refuses to guess.
func hysteriaAuthPassword(y string) (string, bool) {
	var (
		inAuth, seen bool
		indent       string
		fields       = map[string]string{}
	)
	for _, line := range strings.Split(y, "\n") {
		line = strings.TrimRight(line, "\r")
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		lead := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		if lead == "" {
			key, val, _ := strings.Cut(t, ":")
			inAuth, indent = key == "auth", ""
			if !inAuth {
				continue
			}
			if seen {
				return "", false // a second auth key
			}
			seen = true
			switch v := strings.TrimSpace(val); {
			case v == "" || strings.HasPrefix(v, "#"): // a block follows
			case strings.HasPrefix(v, "{"):
				m, ok := yamlFlowMap(v)
				if !ok {
					return "", false
				}
				fields, inAuth = m, false
			default:
				return "", false
			}
			continue
		}
		if !inAuth {
			continue
		}
		if indent == "" {
			indent = lead
		}
		key, raw, ok := strings.Cut(t, ":")
		if lead != indent || !ok || (key != "type" && key != "password") { // deeper levels, other keys
			continue
		}
		v, ok := yamlScalar(raw)
		if !ok {
			return "", false
		}
		fields[key] = v
	}
	if fields["type"] != "password" || fields["password"] == "" {
		return "", false
	}
	return fields["password"], true
}

// yamlQuotedEnd: the index of the quote that closes the quoted scalar s starts with (-1: none) —
// a backslash escapes in double quotes, a doubled quote is one in single quotes.
func yamlQuotedEnd(s string) int {
	q := s[0]
	for i := 1; i < len(s); i++ {
		switch {
		case q == '"' && s[i] == '\\':
			i++
		case s[i] == q && q == '\'' && i+1 < len(s) && s[i+1] == '\'':
			i++
		case s[i] == q:
			return i
		}
	}
	return -1
}

// yamlScalar: the value of a one-line YAML scalar — double-quoted (Go escapes, as config's
// yamlPassword renders), single-quoted (a doubled quote stands for one) or plain (a # after a
// blank starts a comment); ok false for a malformed quoted one or what is no scalar (a flow
// collection, an anchor, a block indicator).
func yamlScalar(raw string) (string, bool) {
	v := strings.TrimSpace(raw)
	switch {
	case v == "":
		return "", true
	case v[0] == '"' || v[0] == '\'':
		end := yamlQuotedEnd(v)
		if end < 0 {
			return "", false
		}
		if tail := strings.TrimSpace(v[end+1:]); tail != "" && !strings.HasPrefix(tail, "#") {
			return "", false
		}
		if v[0] == '\'' {
			return strings.ReplaceAll(v[1:end], "''", "'"), true
		}
		u, err := strconv.Unquote(v[:end+1])
		return u, err == nil
	case strings.ContainsRune("{[&*!|>%@`", rune(v[0])):
		return "", false
	}
	for i := 1; i < len(v); i++ {
		if v[i] == '#' && (v[i-1] == ' ' || v[i-1] == '\t') {
			return strings.TrimSpace(v[:i]), true
		}
	}
	return v, true
}

// yamlFlowMap: the scalar entries of a one-line YAML flow map ({k: v, k: "v"}, a comment after
// it allowed); ok false for anything else (nested collections included).
func yamlFlowMap(v string) (map[string]string, bool) {
	m := map[string]string{}
	i := 1 // after "{"
	skip := func() {
		for i < len(v) && (v[i] == ' ' || v[i] == '\t') {
			i++
		}
	}
	for {
		skip()
		if i < len(v) && v[i] == '}' {
			i++
			break
		}
		j := strings.IndexAny(v[i:], ":,{}[]\"'")
		if j < 0 || v[i+j] != ':' {
			return nil, false
		}
		key := strings.TrimSpace(v[i : i+j])
		i += j + 1
		skip()
		end := strings.IndexAny(v[i:], ",}")
		if i < len(v) && (v[i] == '"' || v[i] == '\'') {
			end = yamlQuotedEnd(v[i:]) + 1
		}
		if key == "" || end <= 0 {
			return nil, false
		}
		val, ok := yamlScalar(v[i : i+end])
		if !ok {
			return nil, false
		}
		m[key] = val
		i += end
		skip()
		switch {
		case i < len(v) && v[i] == ',':
			i++
			continue
		case i < len(v) && v[i] == '}':
			i++
		default:
			return nil, false
		}
		break
	}
	if tail := strings.TrimSpace(v[i:]); tail != "" && !strings.HasPrefix(tail, "#") {
		return nil, false
	}
	return m, true
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
