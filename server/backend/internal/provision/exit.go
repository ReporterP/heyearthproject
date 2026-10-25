package provision

import (
	"context"
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/config"
)

// File is one file a step installs.
type File struct {
	Path string
	Data []byte
	Mode uint32
}

// ExitRecipe: spec §2 steps 1–9 for one exit. Every step converges (verify → apply → verify): a
// file is written only when its content or mode differs, a service restarts only when its files
// or binary changed (restart marker first, then the two-sample check), and the node goes into
// routing only after this job checked both paths — or the TCP path alone when UDP to the exit is
// choked (Review Focus 3). A second run on a converged exit writes and restarts nothing. On a
// live exit the steps that restart a path users ride on (hysteria, xray, the Hub's client) are
// checked end to end around the change (guardPath), one after another.
func ExitRecipe(o ExitOptions) Recipe {
	var hyDown error // set by path_hy's Apply when the main tunnel does not answer; path_tcp decides
	r := Recipe{
		{Name: "preflight", Verify: exitPreflight},
		{Name: "hub_key", Apply: hubKeyApply, Verify: hubKeyVerify},
		{Name: "reality_keys", Apply: keysApply, Verify: keysVerify},
		{Name: "base", Apply: baseApply, Verify: baseVerify},
		{Name: "firewall", Apply: firewallApply(o)},
		{Name: "hysteria", Apply: guardPath(hyPath, hysteriaApply(o)), Verify: hysteriaVerify(o), Rollback: unitRollback("hysteria2")},
		{Name: "xray", Apply: guardPath(vrPath(o), xrayApply(o)), Verify: xrayVerify(o), Rollback: unitRollback("xray")},
	}
	if o.MTProto != nil {
		r = append(r, Step{Name: "mtproxy", Apply: mtproxyApply(*o.MTProto), Verify: mtproxyVerify(*o.MTProto)})
	}
	return append(r,
		Step{Name: "node_agent", Apply: agentApply(o), Verify: agentVerify(o), Rollback: unitRollback("node-agent")},
		Step{Name: "save_tunnel", Apply: tunnelApply, Verify: tunnelVerify},
		Step{Name: "hub_client", Apply: guardPath(hyPath, hubClientApply), Verify: hubClientVerify, Rollback: hubClientRollback},
		Step{Name: "path_hy", Apply: pathHyDegraded(&hyDown), Verify: pathHysteria},
		Step{Name: "path_tcp", Verify: pathTCP(o, &hyDown)},
		Step{Name: "activate", Apply: activateApply, Verify: lifecycleIs("online")},
	)
}

// recipeForSetup: the setup kinds of an exit. provision takes a node that is not in service
// (pending, failed, offline, or provisioning when resumed) through provisioning → online, a
// failure leaves it failed; reprovision converges a working (online) exit and never touches its
// lifecycle; verify only compares. Everything the recipe renders is checked here, so a refused
// recipe creates no job.
func recipeForSetup(kind string, n Node, st *State) (Recipe, error) {
	if n.Role != "exit" {
		return nil, fmt.Errorf("настройка роли «%s» появится вместе с резервными входами", n.Role)
	}
	switch kind {
	case KindProvision:
		if n.Lifecycle == "online" || n.Lifecycle == "deprovisioning" {
			return nil, fmt.Errorf("нода в состоянии «%s»: для работающей — «Переустановить», для выводимой — дождитесь конца вывода", n.Lifecycle)
		}
	case KindReprovision:
		if n.Lifecycle != "online" {
			return nil, fmt.Errorf("переустановка — только для работающей ноды, а она «%s»", n.Lifecycle)
		}
	case KindVerify:
	default:
		return nil, fmt.Errorf("неизвестный вид задачи %q", kind)
	}
	// The renderers' contract (config.ValidateExitIP): ExitHysteriaServerYAML cannot fail and would
	// silently leave out the rules that refuse the exit's own address.
	if err := config.ValidateExitIP(n.IP); err != nil {
		return nil, fmt.Errorf("адрес ноды не подходит для выхода: %w", err)
	}
	if err := st.Exit.validate(); err != nil {
		return nil, err
	}
	if kind == KindProvision {
		st.Begin = func(ctx context.Context) error {
			return st.Hooks.SetLifecycle(ctx, n.ID, []string{"pending", "failed", "offline", "provisioning"}, "provisioning")
		}
		st.Fail = func(ctx context.Context) { _ = st.Hooks.SetLifecycle(ctx, n.ID, []string{"provisioning"}, "failed") }
	}
	return ExitRecipe(st.Exit), nil
}

// validate: what every exit recipe needs from the wiring (API / CLI).
func (o ExitOptions) validate() error {
	switch {
	case o.NodeAgent == nil:
		return errors.New("не задан источник бинарника node-agent на Hub")
	case o.CA == nil:
		return errors.New("не задан CA node-agent на Hub")
	case strings.TrimSpace(o.HubXray) == "":
		return errors.New("не задан xray Hub для проверки запасного пути")
	}
	if _, err := config.ExitAgentGuardConf(o.HubIPs); err != nil {
		return fmt.Errorf("адреса Hub не определены — агент ноды остался бы без защиты или без связи с Hub: %w", err)
	}
	return nil
}

// exitIP is the node's address as the templates render it (recipeForSetup validated it).
func exitIP(s *State) string {
	if ip := net.ParseIP(strings.TrimSpace(s.Node.IP)); ip != nil {
		return ip.String()
	}
	return s.Node.IP
}

// ── preflight ──

// Check is one preflight check (the wizard shows the list; the recipe's first step fails on any).
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

var checkNames = map[string]string{"os": "Операционная система", "arch": "Архитектура", "root": "Вход под root",
	"disk": "Свободное место", "internet": "Выход в интернет", "port_50443": "UDP 50443 (основной туннель)",
	"port_10443": "TCP 10443 (запасной туннель)", "port_50052": "TCP 50052 (агент)"}

// maxClockSkew: node certificates are backdated an hour, Reality refuses clients whose clock is
// off by more than its window — two minutes keeps both far away.
const maxClockSkew = 2 * time.Minute

// Preflight runs the read-only checks (the wizard calls it synchronously, the recipe as step 1).
func Preflight(ctx context.Context, r Remote, now time.Time) ([]Check, error) {
	out, err := r.Run(ctx, config.ExitPreflightScript())
	if err != nil {
		return nil, err
	}
	return parsePreflight(out, now), nil
}

func parsePreflight(out string, now time.Time) []Check {
	var cs []Check
	sawTime := false
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(strings.TrimSpace(line), " ", 4)
		switch {
		case len(f) >= 2 && f[0] == "PF_TIME":
			ts, err := strconv.ParseInt(f[1], 10, 64)
			if err != nil {
				continue
			}
			sawTime = true
			d := now.Sub(time.Unix(ts, 0)).Abs().Round(time.Second)
			cs = append(cs, Check{Name: "Часы сервера", OK: d <= maxClockSkew, Detail: "расхождение с Hub " + d.String()})
		case len(f) >= 3 && f[0] == "PF":
			c := Check{Name: checkNames[f[1]], OK: f[2] == "ok"}
			if c.Name == "" {
				c.Name = f[1]
			}
			if len(f) == 4 {
				c.Detail = f[3]
			}
			cs = append(cs, c)
		}
	}
	if !sawTime {
		cs = append(cs, Check{Name: "Часы сервера", Detail: "время сервера не прочитано"})
	}
	return cs
}

func failedChecks(cs []Check) []string {
	var bad []string
	for _, c := range cs {
		if !c.OK {
			bad = append(bad, strings.TrimSpace(c.Name+": "+c.Detail))
		}
	}
	return bad
}

func exitPreflight(ctx context.Context, r Remote, s *State) error {
	cs, err := Preflight(ctx, r, s.Now())
	if err != nil {
		return err
	}
	for _, c := range cs {
		mark := "✓"
		if !c.OK {
			mark = "✗"
		}
		s.Printf("%s %s %s", mark, c.Name, c.Detail)
	}
	if bad := failedChecks(cs); len(bad) > 0 {
		return fmt.Errorf("сервер не подходит: %s", strings.Join(bad, "; "))
	}
	return nil
}

// ── Hub key, base, firewall ──

func hubKeyApply(ctx context.Context, r Remote, s *State) error {
	pub, err := EnsureHubKey(ctx, s.Hub)
	if err != nil {
		return err
	}
	if err := runPrint(config.AuthorizedKeyScript(pub))(ctx, r, s); err != nil {
		return err
	}
	s.Printf("ключ Hub %s на ноде; дальше Hub входит по нему, пароль остаётся запасным", HubKeyFingerprint(pub))
	return nil
}

func hubKeyVerify(ctx context.Context, r Remote, s *State) error {
	pub, err := EnsureHubKey(ctx, s.Hub)
	if err != nil {
		return err
	}
	kv, err := runKV(ctx, r, config.AuthorizedKeyCheckScript(pub))
	if err != nil {
		return err
	}
	if kv["PRESENT"] != "1" {
		return errors.New("ключа Hub на ноде нет")
	}
	return nil
}

var sysctlFile = File{config.ExitSysctlPath, []byte(config.ExitSysctlConf), 0o644}

func baseApply(ctx context.Context, r Remote, s *State) error {
	if _, err := r.PutFile(ctx, sysctlFile.Path, sysctlFile.Data, sysctlFile.Mode); err != nil {
		return err
	}
	return runPrint(config.ExitBaseScript)(ctx, r, s)
}

func baseVerify(ctx context.Context, r Remote, s *State) error {
	if err := filesMatch(ctx, r, []File{sysctlFile}); err != nil {
		return err
	}
	kv, err := runKV(ctx, r, config.ExitBaseCheckScript)
	if err != nil {
		return err
	}
	if kv["BBR"] != "1" {
		return errors.New("BBR не включён")
	}
	if kv["PKGS"] != "1" {
		return errors.New("не хватает пакетов (curl, unzip, nftables, openssl, ca-certificates)")
	}
	return nil
}

// firewallApply has no Verify: it runs on every pass and never duplicates a rule (ufw skips
// existing ones, iptables -C first — config.ExitFirewallScript).
func firewallApply(o ExitOptions) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		mt := 0
		if o.MTProto != nil {
			mt = o.MTProto.Port
		}
		return runPrint(config.ExitFirewallScript(s.Node.SSHPort, o.HubIPs, mt))(ctx, r, s)
	}
}

// ── files: content AND mode, as PutFile compares them ──

// fileState is what config.SumsScript reports for one existing file.
type fileState struct{ sum, mode string }

func remoteStates(ctx context.Context, r Remote, files []File) (map[string]fileState, error) {
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	out, err := r.Run(ctx, config.SumsScript(paths...))
	if err != nil {
		return nil, err
	}
	states := map[string]fileState{}
	for _, l := range strings.Split(out, "\n") {
		head, path, ok := strings.Cut(strings.TrimRight(l, "\r"), "  ")
		if !ok {
			continue
		}
		if sum, mode, ok := strings.Cut(head, " "); ok {
			states[path] = fileState{sum, mode}
		}
	}
	return states, nil
}

// privateMode: no group or other permission bits (an unreadable mode counts as not private).
func privateMode(mode string) bool {
	m, err := strconv.ParseUint(mode, 8, 32)
	return err == nil && m&0o077 == 0
}

// filesMatch: every file exists with exactly this content and mode; the error names all that differ.
func filesMatch(ctx context.Context, r Remote, files []File) error {
	states, err := remoteStates(ctx, r, files)
	if err != nil {
		return err
	}
	var missing, differ, modes []string
	for _, f := range files {
		cur, ok := states[f.Path]
		switch {
		case !ok:
			missing = append(missing, f.Path)
		case cur.sum != sha256Hex(f.Data):
			differ = append(differ, f.Path)
		case cur.mode != modeString(f.Mode):
			modes = append(modes, fmt.Sprintf("%s (%s, нужны %s)", f.Path, cur.mode, modeString(f.Mode)))
		}
	}
	var parts []string
	if len(missing) > 0 {
		parts = append(parts, "нет "+strings.Join(missing, ", "))
	}
	if len(differ) > 0 {
		parts = append(parts, "отличаются от эталона "+strings.Join(differ, ", "))
	}
	if len(modes) > 0 {
		parts = append(parts, "права доступа "+strings.Join(modes, ", "))
	}
	if len(parts) > 0 {
		return errors.New(strings.Join(parts, "; "))
	}
	return nil
}

// prepareWrites runs before files are written. A unit's restart marker is set with every content
// change about to happen (config.MarkRestartScript: an SSH drop between the write and the restart
// loses neither the restart nor the rollback of a resumed job). Mode-only changes are left out:
// they need neither a restart nor an undo, and a marker entry whose "new" content is already on
// disk would make a rollback restore a stale .bak. An existing file whose new mode is private but
// whose current one is not loses its group/other bits first, .bak included (PutFile's .bak keeps
// the previous mode). unit "" = no marker (a file no service reads at start).
func prepareWrites(ctx context.Context, r Remote, unit string, files []File, states map[string]fileState) error {
	var writes []config.PendingWrite
	var private []string
	for _, f := range files {
		cur, ok := states[f.Path]
		if sum := sha256Hex(f.Data); !ok || cur.sum != sum {
			writes = append(writes, config.PendingWrite{Path: f.Path, SHA256: sum})
		}
		if ok && f.Mode&0o077 == 0 && !privateMode(cur.mode) {
			private = append(private, f.Path)
		}
	}
	if unit != "" && len(writes) > 0 {
		if _, err := r.Run(ctx, config.MarkRestartScript(unit, writes)); err != nil {
			return err
		}
	}
	if len(private) > 0 {
		if _, err := r.Run(ctx, config.PrivateModeScript(private...)); err != nil {
			return err
		}
	}
	return nil
}

// installUnit writes a unit's files (prepareWrites first) and runs converge — the unit's
// config.UnitConvergeScript or UnitConvergeSoftScript, which restarts it only when its marker is
// set or it is down. Returns converge's output (also printed).
func installUnit(ctx context.Context, r Remote, s *State, unit string, files []File, converge string) (string, error) {
	states, err := remoteStates(ctx, r, files)
	if err != nil {
		return "", err
	}
	if err := prepareWrites(ctx, r, unit, files, states); err != nil {
		return "", err
	}
	for _, f := range files {
		changed, err := r.PutFile(ctx, f.Path, f.Data, f.Mode)
		if err != nil {
			return "", err
		}
		if changed {
			s.noteChanged(unit, f.Path)
			s.Printf("записан %s", f.Path)
		}
	}
	out, err := r.Run(ctx, converge)
	s.Printf("%s", out)
	return out, err
}

func unitVerify(ctx context.Context, r Remote, unit string, files []File) error {
	if err := filesMatch(ctx, r, files); err != nil {
		return err
	}
	kv, err := runKV(ctx, r, config.UnitStateScript(unit))
	if err != nil {
		return err
	}
	switch {
	case kv["MARKER"] == "1":
		return fmt.Errorf("%s ждёт перезапуска с новой конфигурацией", unit)
	case kv["ACTIVE"] != "1":
		return fmt.Errorf("%s не запущен", unit)
	case kv["ENABLED"] != "1":
		return fmt.Errorf("%s не включён в автозапуск", unit)
	}
	return nil
}

func unitRollback(unit string) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		return runPrint(config.UnitRollbackScript(unit, s.changed[unit]))(ctx, r, s)
	}
}

func orNone(v string) string {
	if v == "" {
		return "нет"
	}
	return v
}

// ── hysteria (main tunnel) + port hopping ──

var hopFiles = []File{{config.ExitPortHopConfPath, []byte(config.ExitPortHopConf), 0o644}, {config.ExitPortHopUnitPath, []byte(config.ExitPortHopUnit), 0o644}}

func hysteriaFiles(o ExitOptions, s *State) ([]File, error) {
	if err := config.ValidateExitIP(s.Node.IP); err != nil {
		return nil, err
	}
	return []File{
		{config.ExitHysteriaConfig, []byte(config.ExitHysteriaServerYAML(s.Keys.SSPassword, exitIP(s), o.MTProtoPort)), 0o600},
		{config.ExitHysteriaUnitPath, []byte(config.ExitHysteriaUnit), 0o644},
	}, nil
}

func hysteriaApply(o ExitOptions) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		if err := s.needKeys(); err != nil {
			return err
		}
		files, err := hysteriaFiles(o, s)
		if err != nil {
			return err
		}
		if _, err := installUnit(ctx, r, s, "hysteria2", files, config.UnitConvergeScript("hysteria2", config.ExitHysteriaPrepareScript())); err != nil {
			return err
		}
		// The hop is nft only: a changed rule set is reloaded atomically, hysteria is not restarted
		// (no restart marker). Its files belong to this run's rollback of the step all the same: a
		// hop that breaks the main tunnel goes back with it, rules reloaded (UnitRollbackScript).
		// Not being in the marker, a rollback resumed in a later job leaves them.
		for _, f := range hopFiles {
			changed, err := r.PutFile(ctx, f.Path, f.Data, f.Mode)
			if err != nil {
				return err
			}
			if changed {
				s.noteChanged("hysteria2", f.Path)
				s.Printf("записан %s", f.Path)
			}
		}
		return runPrint(config.ExitPortHopApplyScript)(ctx, r, s)
	}
}

func hysteriaVerify(o ExitOptions) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		if err := s.needKeys(); err != nil {
			return err
		}
		files, err := hysteriaFiles(o, s)
		if err != nil {
			return err
		}
		if err := unitVerify(ctx, r, "hysteria2", append(files, hopFiles...)); err != nil {
			return err
		}
		kv, err := runKV(ctx, r, config.HysteriaCheckScript)
		if err != nil {
			return err
		}
		switch {
		case kv["VERSION"] != config.HysteriaVersion:
			return fmt.Errorf("hysteria %s, нужна %s", orNone(kv["VERSION"]), config.HysteriaVersion)
		case kv["LISTEN"] != "1":
			return errors.New("hysteria не слушает UDP 50443")
		case kv["HOP"] != "1":
			return errors.New("правило прыжков портов не применено")
		}
		return nil
	}
}

// ── xray (TCP fallback, VLESS Reality :10443) under its kernel egress guard ──

// xrayFiles: the guard's files and xray's resolv.conf come first — the new xray.service Requires=
// the guard unit and bind-mounts the resolv.conf, so an interrupted write never leaves a unit
// pointing at files that are not there yet. config.ExitXrayPrepareScript (the converge extra)
// creates the user xray and loads the guard before xray restarts.
func xrayFiles(o ExitOptions, s *State) ([]File, error) {
	cfg, err := config.ExitXrayConfigJSON(s.Keys, exitIP(s), o.MTProtoPort)
	if err != nil {
		return nil, err
	}
	guard, err := config.ExitXrayGuardConf(exitIP(s), o.MTProtoPort)
	if err != nil {
		return nil, err
	}
	return []File{
		{config.ExitXrayResolvConfPath, []byte(config.ExitXrayResolvConf), 0o644},
		{config.ExitXrayGuardConfPath, []byte(guard), 0o644},
		{config.ExitXrayGuardUnitPath, []byte(config.ExitXrayGuardUnit), 0o644},
		{config.ExitXrayConfig, cfg, 0o600},
		{config.ExitXrayUnitPath, []byte(config.ExitXrayUnit), 0o644},
	}, nil
}

func xrayApply(o ExitOptions) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		if err := s.needKeys(); err != nil {
			return err
		}
		files, err := xrayFiles(o, s)
		if err != nil {
			return err
		}
		_, err = installUnit(ctx, r, s, "xray", files, config.UnitConvergeScript("xray", config.ExitXrayPrepareScript()))
		return err
	}
}

func xrayVerify(o ExitOptions) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		if err := s.needKeys(); err != nil {
			return err
		}
		files, err := xrayFiles(o, s)
		if err != nil {
			return err
		}
		if err := unitVerify(ctx, r, "xray", files); err != nil {
			return err
		}
		kv, err := runKV(ctx, r, config.XrayCheckScript)
		if err != nil {
			return err
		}
		switch want := strings.TrimPrefix(config.XrayExitVersion, "v"); {
		case kv["VERSION"] != want:
			return fmt.Errorf("xray %s, нужна %s", orNone(kv["VERSION"]), want)
		case kv["LISTEN"] != "1":
			return errors.New("xray не слушает TCP 10443")
		case kv["GUARD"] != "1":
			return errors.New("защита исходящих xray (nft table inet " + config.ExitXrayGuardTable + ") не загружена")
		}
		return nil
	}
}

// ── node-agent: own certificate, Hub-only guard ──

// agentProbeTimeout bounds Hooks.ProbeAgent inside agentVerify (tests shorten it).
var agentProbeTimeout = 20 * time.Second

// agentFiles: the Hub-only guard first (node-agent.service Requires= it), then the binary, the
// Hub CA and the unit. The node's certificate and key are added by agentApply when they must be
// (re)issued.
func agentFiles(o ExitOptions) (files []File, ca, caKey []byte, err error) {
	bin, err := o.NodeAgent()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("бинарник node-agent на Hub: %w", err)
	}
	if ca, caKey, err = o.CA(); err != nil {
		return nil, nil, nil, fmt.Errorf("CA node-agent на Hub: %w", err)
	}
	guard, err := config.ExitAgentGuardConf(o.HubIPs)
	if err != nil {
		return nil, nil, nil, err
	}
	return []File{
		{config.ExitAgentGuardConfPath, []byte(guard), 0o644},
		{config.ExitAgentGuardUnitPath, []byte(config.ExitAgentGuardUnit), 0o644},
		{config.NodeAgentBinary, bin, 0o755},
		{config.NodeAgentCACert, ca, 0o644},
		{config.NodeAgentUnitPath, []byte(config.NodeAgentUnit), 0o644},
	}, ca, caKey, nil
}

// agentCert reads the node's certificate and its key's state: certErr is why the certificate is
// not good for this node (nil: chains to the Hub CA, names the node, 30+ days left, and is the
// pair of the key on the node), key the key file's state (ok false: missing). The pair is
// compared through the key's public half (config.KeyPublicScript): the key never leaves the node.
func agentCert(ctx context.Context, r Remote, s *State, ca []byte) (certErr error, key fileState, keyOK bool, err error) {
	cur, err := r.Run(ctx, config.CatScript(config.NodeAgentCert))
	if err != nil {
		return nil, fileState{}, false, err
	}
	states, err := remoteStates(ctx, r, []File{{Path: config.NodeAgentKey}})
	if err != nil {
		return nil, fileState{}, false, err
	}
	key, keyOK = states[config.NodeAgentKey]
	if certErr = CheckNodeCert([]byte(cur), ca, exitIP(s), s.Now()); certErr != nil || !keyOK {
		return certErr, key, keyOK, nil
	}
	pub, err := r.Run(ctx, config.KeyPublicScript(config.NodeAgentKey))
	if err != nil {
		return nil, fileState{}, false, err
	}
	if strings.TrimSpace(pub) == "NO_OPENSSL" {
		// Not a reason to issue: a pair issued on every run would restart node-agent every time.
		return nil, fileState{}, false, errors.New("на ноде нет openssl — пару сертификат/ключ агента не проверить (его ставит шаг «" + Title("base") + "»)")
	}
	return keyPairErr([]byte(cur), pub), key, keyOK, nil
}

// keyPairErr is nil when pubPEM (the "PUBLIC KEY" PEM KeyPublicScript printed for the node's
// key) is the public key of certPEM.
func keyPairErr(certPEM []byte, pubPEM string) error {
	b, _ := pem.Decode([]byte(pubPEM))
	if b == nil || b.Type != "PUBLIC KEY" {
		return errors.New("закрытый ключ агента не читается")
	}
	pub, err := x509.ParsePKIXPublicKey(b.Bytes)
	if err != nil {
		return errors.New("закрытый ключ агента не читается")
	}
	cert, err := parseCert(certPEM)
	if err != nil {
		return fmt.Errorf("сертификата нет или он повреждён: %w", err)
	}
	if cp, ok := cert.PublicKey.(interface{ Equal(crypto.PublicKey) bool }); !ok || !cp.Equal(pub) {
		return errors.New("сертификат и закрытый ключ агента — не пара (ключ заменён или запись пары прервалась)")
	}
	return nil
}

func agentApply(o ExitOptions) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		files, ca, caKey, err := agentFiles(o)
		if err != nil {
			return err
		}
		certErr, key, keyOK, err := agentCert(ctx, r, s, ca)
		if err != nil {
			return err
		}
		switch {
		case certErr != nil || !keyOK:
			why := certErr
			if why == nil {
				why = errors.New("нет закрытого ключа")
			}
			cert, k, err := IssueNodeCert(ca, caKey, exitIP(s), s.Now())
			if err != nil {
				return err
			}
			s.Printf("сертификат агента: %v — выпускаю собственный для %s", why, exitIP(s))
			// The key before the certificate: cut in between, the old certificate is not the new
			// key's pair (agentCert compares them) and the next run issues a fresh pair.
			files = append(files, File{config.NodeAgentKey, k, 0o600}, File{config.NodeAgentCert, cert, 0o644})
		case !privateMode(key.mode):
			if _, err := r.Run(ctx, config.PrivateModeScript(config.NodeAgentKey)); err != nil {
				return err
			}
			s.Printf("закрытый ключ агента был доступен не только root (%s) — права исправлены", key.mode)
		}
		_, err = installUnit(ctx, r, s, "node-agent", files, config.UnitConvergeScript("node-agent", config.ExitAgentGuardApplyScript))
		return err
	}
}

func agentVerify(o ExitOptions) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		files, ca, _, err := agentFiles(o)
		if err != nil {
			return err
		}
		certErr, key, keyOK, err := agentCert(ctx, r, s, ca)
		if err != nil {
			return err
		}
		switch {
		case certErr != nil:
			return fmt.Errorf("сертификат агента: %w", certErr)
		case !keyOK:
			return errors.New("нет закрытого ключа агента")
		case !privateMode(key.mode):
			return fmt.Errorf("закрытый ключ агента доступен не только root (права %s)", key.mode)
		}
		if err := unitVerify(ctx, r, "node-agent", files); err != nil {
			return err
		}
		kv, err := runKV(ctx, r, config.NodeAgentCheckScript)
		if err != nil {
			return err
		}
		switch {
		case kv["GUARD"] != "1":
			return errors.New("защита агента (TCP 50052 только с адресов Hub, nft table inet " + config.ExitAgentGuardTable + ") не загружена")
		case kv["LISTEN"] != "1":
			return errors.New("node-agent не слушает TCP 50052")
		}
		return probeAgent(ctx, s)
	}
}

// probeAgent: the Hub's own mTLS call to the agent, bounded by agentProbeTimeout. Its own timeout
// means "the agent does not answer" — a reason to Apply, so the error never wraps the context
// error (inconclusive would take it for the job's); the job's own context running out stays
// inconclusive.
func probeAgent(ctx context.Context, s *State) error {
	if s.Hooks.ProbeAgent == nil {
		return nil
	}
	pctx, cancel := context.WithTimeout(ctx, agentProbeTimeout)
	defer cancel()
	err := s.Hooks.ProbeAgent(pctx, exitIP(s)) // a panic reaches the runner's protect: inconclusive
	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil:
		return ctx.Err()
	case pctx.Err() != nil:
		return fmt.Errorf("Hub не дождался ответа агента по mTLS за %s (TCP 50052 с адресов Hub должен быть открыт, в том числе в панели хостера)", agentProbeTimeout)
	}
	return fmt.Errorf("Hub не достучался до агента по mTLS: %v (TCP 50052 с адресов Hub должен быть открыт, в том числе в панели хостера)", err)
}

// ── DB tunnel row, Hub client, end-to-end paths ──

func wantTunnel(s *State) Tunnel {
	return Tunnel{UUID: s.Keys.UUID, PublicKey: s.Keys.PublicKey, ShortID: s.Keys.ShortID, SNI: config.ExitRealitySNI, Password: s.Keys.SSPassword}
}

func tunnelVerify(ctx context.Context, _ Remote, s *State) error {
	if err := s.needKeys(); err != nil {
		return err
	}
	cur, err := s.Hooks.LoadTunnel(ctx, s.Node.ID)
	if err != nil {
		return err
	}
	if cur != wantTunnel(s) {
		return errors.New("данные туннеля в базе не совпадают с ключами на ноде (или в базе ещё лежит закрытый ключ)")
	}
	return nil
}

// tunnelApply: on a working exit the Hub's xray must learn the new data; a node being set up is
// announced once, by activate.
func tunnelApply(ctx context.Context, _ Remote, s *State) error {
	if err := s.needKeys(); err != nil {
		return err
	}
	changed, err := s.Hooks.SaveTunnel(ctx, s.Node.ID, wantTunnel(s))
	if err != nil {
		return err
	}
	if changed && s.Node.Lifecycle == "online" && s.Hooks.RequestSync != nil {
		s.Hooks.RequestSync()
		s.Printf("данные туннеля обновлены — Xray на Hub получит их при синхронизации")
	}
	return nil
}

func hubUnit(s *State) string { return config.HubClientUnit(s.Node.ID) }

func hubClientFiles(s *State) []File {
	id := s.Node.ID
	return []File{
		{config.HubClientConfigPath(id), []byte(config.HubHysteriaClientYAML(id, exitIP(s), s.Keys.SSPassword, 1088+id)), 0o600},
		{"/etc/systemd/system/" + hubUnit(s) + ".service", []byte(config.HubHysteriaClientUnit(id, exitIP(s))), 0o644},
	}
}

// hubClientApply converges the Hub's hysteria client of the exit with the soft converge: a client
// that cannot hold its connection (UDP to the exit choked — it crash-loops) is a path problem,
// reported here and judged by path_hy/path_tcp, not a failed setup (Review Focus 3).
func hubClientApply(ctx context.Context, _ Remote, s *State) error {
	if err := s.needKeys(); err != nil {
		return err
	}
	out, err := installUnit(ctx, s.Hub, s, hubUnit(s), hubClientFiles(s), config.UnitConvergeSoftScript(hubUnit(s), config.HysteriaEnsureScript()))
	if err != nil {
		return err
	}
	if parseKV(out)["HEALTH"] == "0" {
		s.warnf("клиент туннеля на Hub перезапущен, но соединение с выходом %s не держится — похоже, UDP «Россия → заграница» задушен; "+
			"systemd продолжает попытки, пути проверят следующие шаги", exitIP(s))
	}
	return nil
}

// hubClientVerify: files, no pending restart, enabled, and running — or in the pause of the
// crash loop of a client whose UDP path is down (systemd keeps restarting it with these files;
// the path itself is path_hy's matter). A stopped or failed client is not accepted.
func hubClientVerify(ctx context.Context, _ Remote, s *State) error {
	if err := s.needKeys(); err != nil {
		return err
	}
	if err := filesMatch(ctx, s.Hub, hubClientFiles(s)); err != nil {
		return err
	}
	u := hubUnit(s)
	kv, err := runKV(ctx, s.Hub, config.UnitStateScript(u))
	if err != nil {
		return err
	}
	switch {
	case kv["MARKER"] == "1":
		return fmt.Errorf("%s ждёт перезапуска с новой конфигурацией", u)
	case kv["ENABLED"] != "1":
		return fmt.Errorf("%s не включён в автозапуск", u)
	case kv["ACTIVE"] != "1" && kv["RETRY"] != "1":
		return fmt.Errorf("%s не запущен", u)
	case kv["ACTIVE"] != "1":
		s.Printf("%s перезапускается — соединение с выходом не держится (вероятно, задушен UDP); путь проверит шаг «%s»", u, Title("path_hy"))
	}
	return nil
}

func hubClientRollback(ctx context.Context, _ Remote, s *State) error {
	return runPrint(config.UnitRollbackScript(hubUnit(s), s.changed[hubUnit(s)]))(ctx, s.Hub, s)
}

// ── end-to-end paths: the probes, and the guard around a live exit's risky restarts ──

// pathCheck is one end-to-end path users' traffic rides on: its owner-facing name and its probe
// from the Hub (ok: generate_204 answered; code: what came back).
type pathCheck struct {
	name  string
	probe func(ctx context.Context, s *State) (ok bool, code string, err error)
}

// hyPath: the main tunnel — generate_204 through the Hub's hysteria client of the exit (SOCKS
// 127.0.0.1:1088+id).
var hyPath = pathCheck{"основной туннель", func(ctx context.Context, s *State) (bool, string, error) {
	kv, err := runKV(ctx, s.Hub, config.SocksProbeScript(1088+s.Node.ID))
	if err != nil {
		return false, "", err
	}
	return kv["HTTP"] == "204", orNone(kv["HTTP"]), nil
}}

// vrPath: the TCP fallback — generate_204 through a short-lived xray on the Hub over VLESS
// Reality to the exit's :10443 (exit-<id>.vr). The probe config carries the tunnel UUID: it is
// put at VRProbeConfigPath and removed by the probe script.
func vrPath(o ExitOptions) pathCheck {
	return pathCheck{"запасной TCP-путь", func(ctx context.Context, s *State) (bool, string, error) {
		if err := s.needKeys(); err != nil {
			return false, "", err
		}
		port := config.VRProbeSocksPort(s.Node.ID)
		cfg, err := config.ExitRealityProbeConfig(port, config.RealityProbe{ExitIP: exitIP(s), UUID: s.Keys.UUID,
			PublicKey: s.Keys.PublicKey, ShortID: s.Keys.ShortID, SNI: config.ExitRealitySNI})
		if err != nil {
			return false, "", err
		}
		path := config.VRProbeConfigPath(s.Node.ID)
		if _, err := s.Hub.PutFile(ctx, path, cfg, 0o600); err != nil {
			return false, "", err
		}
		kv, err := runKV(ctx, s.Hub, config.VRProbeScript(o.HubXray, path, port))
		if err != nil {
			return false, "", err
		}
		return kv["HTTP"] == "204", orNone(kv["HTTP"]), nil
	}}
}

// liveExit: the job converges an exit that carries users (reprovision runs only on online nodes).
func liveExit(s *State) bool { return s.Kind == KindReprovision && s.Node.Lifecycle == "online" }

// After a change the path gets pathAfterRounds probe runs (each up to three generate_204) with
// pathAfterPause between: the Hub's hysteria client notices a restarted server only when its QUIC
// connection times out (about 30 s), then reconnects. A rollback must not hang on that.
var (
	pathAfterRounds = 2
	pathAfterPause  = 15 * time.Second
)

// guardPath wraps the Apply of a step whose restart can cut a path users ride on. On a live exit
// the path is probed end to end before the change and after it: a path that answered before and
// is dead after fails the step, and the runner rolls it back (the previous files, nft rules
// included, return and the service restarts with them — the node stays online). A path already
// dead before — UDP «Россия → заграница» choked (Review Focus 3) — is no reason for a rollback;
// path_hy / path_tcp judge it. The check after the change is part of the step and the steps run
// one after another, so the next risky restart (xray after hysteria) begins only once this one's
// path answers again: users always keep a working path. Elsewhere (provision: the node is not in
// routing; verify never applies) apply runs as it is.
func guardPath(p pathCheck, apply stepFunc) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		if !liveExit(s) {
			return apply(ctx, r, s)
		}
		before, code, err := p.probe(ctx, s)
		if err != nil {
			return fmt.Errorf("%s не проверен до изменений — ничего не меняю: %w", p.name, err)
		}
		if before {
			s.Printf("%s отвечает до изменений — проверю его и после", p.name)
		} else {
			s.Printf("%s до изменений не отвечает (код %s) — откатывать по нему не буду, путь проверит отдельный шаг", p.name, code)
		}
		if err := apply(ctx, r, s); err != nil {
			return err
		}
		if !before {
			return nil
		}
		for i := range pathAfterRounds {
			if i > 0 {
				if err := s.Sleep(ctx, pathAfterPause); err != nil {
					return err
				}
			}
			ok, c, err := p.probe(ctx, s)
			if err != nil {
				return fmt.Errorf("%s не проверен после изменений: %w", p.name, err)
			}
			if ok {
				s.Printf("%s отвечает и после изменений", p.name)
				return nil
			}
			code = c
		}
		return fmt.Errorf("%s отвечал до изменений и перестал после них (код %s) — изменения шага откатываются", p.name, code)
	}
}

func pathHysteria(ctx context.Context, _ Remote, s *State) error {
	port := 1088 + s.Node.ID
	ok, code, err := hyPath.probe(ctx, s)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("через основной туннель (127.0.0.1:%d) нет ответа 204 (код %s)", port, code)
	}
	s.Printf("generate_204 через 127.0.0.1:%d — 204", port)
	return nil
}

// pathHyDegraded is path_hy's Apply (never in a verify job, where a dead path is drift): with the
// TCP fallback on, a main tunnel that does not answer only warns — UDP «Россия → заграница» may be
// choked while the exit is fine; path_tcp then decides (the exit goes online on its TCP path
// alone, the monitor raises the path alert). With the fallback off the exit would carry nothing:
// the step fails.
func pathHyDegraded(hyDown *error) stepFunc {
	return func(_ context.Context, _ Remote, s *State) error {
		if !s.Node.FallbackEnabled {
			return errors.New("основной туннель не отвечает, а запасной TCP-путь выключен владельцем — выходу некуда вести трафик")
		}
		*hyDown = errors.New("основной туннель не отвечает")
		s.warnf("основной туннель (UDP) до выхода %s не отвечает — похоже, UDP «Россия → заграница» задушен. Выход включится, если ответит "+
			"запасной TCP-путь (следующий шаг): трафик пойдёт по нему, монитор поднимет тревогу о пути, основной туннель вернётся сам, когда UDP восстановится", exitIP(s))
		return fmt.Errorf("%w: основной туннель не отвечает", ErrSkipped)
	}
}

func pathTCP(o ExitOptions, hyDown *error) stepFunc {
	return func(ctx context.Context, _ Remote, s *State) error {
		if !s.Node.FallbackEnabled {
			s.Printf("запасной TCP-путь выключен владельцем — не проверяю")
			return nil
		}
		ok, code, err := vrPath(o).probe(ctx, s)
		if err != nil {
			return err
		}
		if !ok {
			if *hyDown != nil {
				return fmt.Errorf("не отвечают оба пути: основной туннель и запасной TCP (код %s) — выход не включаю", code)
			}
			return fmt.Errorf("через запасной TCP-путь нет ответа 204 (код %s)", code)
		}
		s.Printf("generate_204 через VLESS Reality %s:%d — 204", exitIP(s), config.ExitRealityPort)
		if *hyDown != nil {
			s.Printf("выход работает по одному пути — запасному TCP; основной туннель вернётся сам, когда UDP восстановится")
		}
		return nil
	}
}
