package provision

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/heyearth/backend/internal/config"
	"golang.org/x/crypto/ssh"
)

var testHubPub = func() string {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	sp, _ := ssh.NewPublicKey(pub)
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sp)))
}()

// fakeHost is one host of the recipe tests — a node or the Hub — that remembers what was done
// to it. Operations are recognised by the script's first line "# hp:<op> <args>". Files follow
// PutFile exactly: same content and mode → unchanged; a differing previous version → <path>.bak
// (with its mode); a new file deletes a stale <path>.bak.
type fakeHost struct {
	files      map[string][]byte
	modes      map[string]uint32
	writes     int
	active     map[string]bool
	enabled    map[string]bool
	retry      map[string]bool   // in the pause of a crash loop (SubState auto-restart)
	nft        map[string]bool   // nft tables (inet) loaded in the kernel
	nftRules   map[string]string // table → the rule file content last loaded into the kernel
	marker     map[string]bool
	markWrites map[string]map[string]string // unit → path → sha256 ever recorded by MarkRestartScript (history)
	markFile   map[string]map[string]string // unit → path → sha256 in the marker file now (gone with the marker)
	pathMark   map[string]*fakePathMark     // unit → its path-check marker (config.PathCheckMarker)
	restarts   map[string]int
	authKeys   []string
	ops        []string
	scripts    map[string]string // op → the last script run with it
	seen       func(op string)   // called for every op before it runs (tests watch the routing state)
	dropOnce   string            // the first op with this prefix loses the SSH connection…
	dropped    bool
	down       bool // …and everything after it fails too (no rollback) until the next job dials again
	// dropAfterOutput: the first op with this prefix runs and prints, then the session is lost —
	// the error quotes the output tail, as SSHRemote's does.
	dropAfterOutput string
	mtproxyFail     bool
	mtSecret        string // the secret mtproxy runs with (set by mtproxy-install)
	mtgVersion      string
	noOpenSSL       bool // key-pub answers NO_OPENSSL
	// Hub: UDP to the exits is choked — a hysteria client crash-loops after every (re)start (the
	// real client exits when its first connection fails) and nothing answers through its SOCKS.
	udpChoked bool
	vrFail    bool // Hub: the TCP path probe gets no 204
	// probeDead: a path probe (op "socks-probe <port>" or "vr-probe <port>") gets no 204 when it says so —
	// tests tie a path to what the node runs.
	probeDead func(op string) bool
	// dropAfterPut: the first write of one of these paths lands, then the connection is lost.
	dropAfterPut []string
	// healthy decides whether a (re)started unit runs (nil: always); onStart runs after every
	// successful (re)start — tests tie the Hub's paths to the configs the node's services run with.
	healthy func(unit string) bool
	onStart func(unit string)
	name    string    // trace prefix
	trace   *[]string // every op of this host (and of others sharing it) in order: "<name>: <op>"
}

// errLost is what a dropped SSH session returns (DialNode's SSHRemote wraps ErrConnectionLost).
var errLost = fmt.Errorf("%w: ssh: connection lost", ErrConnectionLost)

func newFakeHost() *fakeHost {
	return &fakeHost{files: map[string][]byte{}, modes: map[string]uint32{}, active: map[string]bool{}, enabled: map[string]bool{},
		retry: map[string]bool{}, nft: map[string]bool{}, nftRules: map[string]string{}, marker: map[string]bool{},
		markWrites: map[string]map[string]string{}, markFile: map[string]map[string]string{}, pathMark: map[string]*fakePathMark{},
		restarts: map[string]int{}, scripts: map[string]string{}, mtgVersion: "2.2.8"}
}

// fakePathMark is a path-check marker: the path's state before the change and its writes.
type fakePathMark struct {
	before bool
	writes map[string]string // path → sha256
}

func (h *fakeHost) note(op string) {
	h.ops = append(h.ops, op)
	if h.trace != nil {
		*h.trace = append(*h.trace, h.name+": "+op)
	}
}

var fakeMarkLine = regexp.MustCompile(`(?m)^l='([0-9a-f]{64})  ([^']*)'`)

// start (re)starts u as systemctl restart + restart_checked would: it runs when healthy says so
// (the journal's "не запустился" otherwise), onStart after a successful start.
func (h *fakeHost) start(u string) error {
	h.restarts[u]++
	if h.healthy != nil && !h.healthy(u) {
		h.active[u] = false
		return errors.New("exit status 1: [unit] " + u + " не запустился")
	}
	h.active[u], h.retry[u] = true, false
	if h.onStart != nil {
		h.onStart(u)
	}
	return nil
}

// loadNft loads a rule file into the kernel (nft -f).
func (h *fakeHost) loadNft(table, path string) {
	h.nft[table], h.nftRules[table] = true, string(h.files[path])
}

// converge models UnitConvergeScript / UnitConvergeSoftScript: the extra loads the unit's guard
// (xray: the egress guard, node-agent: the Hub-only guard) when its file is there; the unit is
// restarted when marked or down. A failed restart keeps the marker (strict) or reports HEALTH=0
// and drops it (soft). A Hub client under udpChoked crash-loops after the restart.
func (h *fakeHost) converge(u string, soft bool) (string, error) {
	switch u {
	case "xray":
		if h.files[config.ExitXrayGuardConfPath] != nil {
			h.loadNft(config.ExitXrayGuardTable, config.ExitXrayGuardConfPath)
			h.enabled[config.ExitXrayGuardService] = true
		}
	case "node-agent":
		if h.files[config.ExitAgentGuardConfPath] != nil {
			h.loadNft(config.ExitAgentGuardTable, config.ExitAgentGuardConfPath)
			h.active[config.ExitAgentGuardService], h.enabled[config.ExitAgentGuardService] = true, true
		}
	}
	h.enabled[u] = true
	if !h.marker[u] && h.active[u] {
		return "[unit] " + u + ": без изменений и работает — без перезапуска\n", nil
	}
	if h.udpChoked && strings.HasPrefix(u, "hysteria2-client-") {
		h.restarts[u]++
		h.active[u], h.retry[u] = false, true
		if !soft {
			h.marker[u] = true
			return "", errors.New("exit status 1: [unit] " + u + " перезапускается сам (NRestarts 1 -> 2)")
		}
		h.clearMarker(u)
		return "HEALTH=0\n", nil
	}
	err := h.start(u)
	switch {
	case err != nil && !soft:
		return "", err
	case err != nil:
		h.clearMarker(u)
		return "HEALTH=0\n", nil
	}
	h.clearMarker(u)
	if soft {
		return "HEALTH=1\n", nil
	}
	return "", nil
}

func (h *fakeHost) clearMarker(u string) {
	delete(h.marker, u)
	delete(h.markFile, u)
}

var (
	fakeRestoreLine = regexp.MustCompile(`(?m)^rb_restore '([^']*)'$`)
	fakeReloadLine  = regexp.MustCompile(`(?m)^rb_reload_nft '([^']*)' '([^']*)'$`)
)

// rollback models config.UnitRollbackScript: the files to undo are the run's changed ones (the
// script's rb_restore lines) and the marker's writes whose file still has the recorded content;
// each gets its .bak back or, new in the run, is removed (a new unit file of the run disabled and
// stopped first). A restored nft rule file of ours is loaded into the kernel again. A unit whose
// own file was new is disabled and stopped; otherwise it restarts with the previous files —
// healthy decides whether it runs, a failed restart fails the rollback with the marker kept.
func (h *fakeHost) rollback(u, script string) (string, error) {
	var undo []string
	for _, m := range fakeRestoreLine.FindAllStringSubmatch(script, -1) {
		undo = append(undo, m[1])
	}
	recorded := func(writes map[string]string) {
		for _, p := range slices.Sorted(maps.Keys(writes)) {
			if d, ok := h.files[p]; ok && sha256Hex(d) == writes[p] {
				undo = append(undo, p)
			}
		}
	}
	if h.marker[u] {
		recorded(h.markFile[u])
	}
	if pm := h.pathMark[u]; pm != nil {
		recorded(pm.writes)
	}
	var back, fresh []string
	for _, p := range undo {
		if slices.Contains(back, p) || slices.Contains(fresh, p) {
			continue
		}
		if b, ok := h.files[p+".bak"]; ok {
			h.files[p], h.modes[p] = append([]byte(nil), b...), h.modes[p+".bak"]
			back = append(back, p)
		} else if _, ok := h.files[p]; ok {
			fresh = append(fresh, p)
		}
	}
	for _, m := range fakeReloadLine.FindAllStringSubmatch(script, -1) {
		if slices.Contains(back, m[1]) {
			h.note("nft-reload " + m[1])
			h.loadNft(m[2], m[1])
		}
	}
	removeFresh := func() {
		for _, p := range fresh {
			if dir, file := path.Split(p); dir == "/etc/systemd/system/" && strings.HasSuffix(file, ".service") {
				nu := strings.TrimSuffix(file, ".service")
				h.active[nu], h.enabled[nu] = false, false
			}
			delete(h.files, p)
			delete(h.modes, p)
		}
	}
	unitFile := "/etc/systemd/system/" + u + ".service"
	switch {
	case len(back) == 0 && len(fresh) == 0:
		delete(h.pathMark, u)
		return "[unit] " + u + ": в этом запуске файлы не менялись — возвращать нечего\n", nil
	case h.files[unitFile] == nil || slices.Contains(fresh, unitFile):
		h.active[u], h.enabled[u] = false, false
		removeFresh()
		h.clearMarker(u)
		delete(h.pathMark, u)
		return "[unit] " + u + ": новая установка снята\n", nil
	}
	removeFresh()
	if err := h.start(u); err != nil {
		return "", err // set -e: both markers kept
	}
	h.clearMarker(u)
	delete(h.pathMark, u)
	return "[unit] " + u + ": возвращена прежняя версия\n", nil
}

func (h *fakeHost) PutFile(_ context.Context, path string, data []byte, mode uint32) (bool, error) {
	h.note("put " + path)
	if h.down {
		return false, errLost
	}
	old, ok := h.files[path]
	if ok && bytes.Equal(old, data) && h.modes[path] == mode {
		return false, nil
	}
	if ok {
		h.files[path+".bak"], h.modes[path+".bak"] = old, h.modes[path]
	} else {
		delete(h.files, path+".bak")
		delete(h.modes, path+".bak")
	}
	h.files[path], h.modes[path] = append([]byte(nil), data...), mode
	h.writes++
	if slices.Contains(h.dropAfterPut, path) {
		h.dropAfterPut, h.down = nil, true
		return false, errLost
	}
	return true, nil
}

func between(s, a, b string) string {
	_, rest, ok := strings.Cut(s, a)
	if !ok {
		return ""
	}
	v, _, _ := strings.Cut(rest, b)
	return v
}

func b01(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func (h *fakeHost) count(op string) int {
	n := 0
	for _, o := range h.ops {
		if o == op {
			n++
		}
	}
	return n
}

// remove deletes files (and their .bak).
func (h *fakeHost) remove(paths ...string) {
	for _, p := range paths {
		for _, q := range []string{p, p + ".bak"} {
			delete(h.files, q)
			delete(h.modes, q)
		}
	}
}

// installMTProxyArgs reads `set -- 'port' 'secret' 'domain'` of MTProxyRunScript.
func installMTProxyArgs(script string) []string {
	for _, l := range strings.Split(script, "\n") {
		if rest, ok := strings.CutPrefix(l, "set -- "); ok {
			f := strings.Fields(rest)
			for i := range f {
				f[i] = strings.Trim(f[i], "'")
			}
			return f
		}
	}
	return nil
}

var (
	fakeYAMLPassword = regexp.MustCompile(`(?m)^  password: .*$`)
	fakeJSONPassword = regexp.MustCompile(`"ssPassword": *"[^"]*"`)
)

func (h *fakeHost) Run(_ context.Context, script string) (string, error) {
	first, _, _ := strings.Cut(script, "\n")
	op := strings.TrimPrefix(first, "# hp:")
	h.note(op)
	h.scripts[op] = script
	if h.seen != nil {
		h.seen(op)
	}
	if h.down {
		return "", errLost
	}
	if h.dropOnce != "" && !h.dropped && strings.HasPrefix(op, h.dropOnce) {
		h.dropped, h.down = true, true
		return "", errLost
	}
	out, err := h.run(op, script)
	if h.dropAfterOutput != "" && strings.HasPrefix(op, h.dropAfterOutput) {
		h.dropAfterOutput, h.down = "", true
		return out, fmt.Errorf("%w: ssh: connection lost: %s", ErrConnectionLost, tail(out, errTail))
	}
	return out, err
}

func (h *fakeHost) run(op, script string) (string, error) {
	f := strings.Fields(op)
	if len(f) == 0 {
		return "", nil
	}
	switch f[0] {
	case "preflight":
		return fmt.Sprintf("PF os ok Ubuntu 24.04\nPF arch ok x86_64\nPF root ok\nPF disk ok 9000 МБ\nPF internet ok\n"+
			"PF port_50443 ok\nPF port_10443 ok\nPF port_50052 ok\nPF_TIME %d\n", testNow.Unix()), nil
	case "cat":
		return string(h.files[f[1]]), nil
	case "sums":
		var b strings.Builder
		for _, p := range f[1:] {
			if d, ok := h.files[p]; ok {
				fmt.Fprintf(&b, "%s %s  %s\n", sha256Hex(d), modeString(h.modes[p]), p)
			}
		}
		return b.String(), nil
	case "mark":
		h.marker[f[1]] = true
		for _, m := range fakeMarkLine.FindAllStringSubmatch(script, -1) {
			for _, mw := range []map[string]map[string]string{h.markWrites, h.markFile} {
				if mw[f[1]] == nil {
					mw[f[1]] = map[string]string{}
				}
				mw[f[1]][m[2]] = m[1]
			}
		}
	case "key-pub": // openssl pkey -pubout on the node: the public half only
		if h.noOpenSSL {
			return "NO_OPENSSL\n", nil
		}
		k, err := parseKey(h.files[f[1]])
		if err != nil {
			return "", nil
		}
		der, err := x509.MarshalPKIXPublicKey(k.Public())
		if err != nil {
			return "", nil
		}
		return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), nil
	case "private":
		for _, p := range f[1:] {
			for _, q := range []string{p, p + ".bak"} {
				if _, ok := h.files[q]; ok {
					h.modes[q] &= 0o700
				}
			}
		}
	case "converge":
		return h.converge(f[1], len(f) > 2 && f[2] == "soft")
	case "unit-state":
		return "ACTIVE=" + b01(h.active[f[1]]) + "\nENABLED=" + b01(h.enabled[f[1]]) + "\nRETRY=" + b01(h.retry[f[1]]) +
			"\nMARKER=" + b01(h.marker[f[1]]) + "\nPATHCHECK=" + b01(h.pathMark[f[1]] != nil) + "\n", nil
	case "path-state":
		pm := h.pathMark[f[1]]
		if pm == nil {
			return "PRESENT=0\n", nil
		}
		var b strings.Builder
		b.WriteString("PRESENT=1\n")
		for _, p := range slices.Sorted(maps.Keys(pm.writes)) {
			b.WriteString("FILE=" + p + "\n")
		}
		return b.String() + "BEFORE=" + b01(pm.before) + "\n", nil
	case "path-start":
		if h.pathMark[f[1]] == nil {
			h.pathMark[f[1]] = &fakePathMark{before: f[2] == "1", writes: map[string]string{}}
		}
	case "path-record":
		if pm := h.pathMark[f[1]]; pm != nil {
			for _, m := range fakeMarkLine.FindAllStringSubmatch(script, -1) {
				pm.writes[m[2]] = m[1]
			}
		}
	case "path-done":
		delete(h.pathMark, f[1])
	case "rollback":
		return h.rollback(f[1], script)
	case "hop-apply":
		h.loadNft(config.ExitPortHopTable, config.ExitPortHopConfPath)
	case "hy-check":
		return "VERSION=" + config.HysteriaVersion + "\nLISTEN=" + b01(h.active["hysteria2"]) + "\nHOP=" + b01(h.files[config.ExitPortHopConfPath] != nil) + "\n", nil
	case "xray-check":
		return "VERSION=" + strings.TrimPrefix(config.XrayExitVersion, "v") + "\nLISTEN=" + b01(h.active["xray"]) +
			"\nGUARD=" + b01(h.nft[config.ExitXrayGuardTable]) + "\n", nil
	case "agent-check":
		return "LISTEN=" + b01(h.active["node-agent"]) + "\nGUARD=" + b01(h.nft[config.ExitAgentGuardTable]) + "\n", nil
	case "base-check":
		return "BBR=1\nPKGS=1\n", nil
	case "authkey":
		if k := between(script, "KEY='", "'"); !slices.Contains(h.authKeys, k) {
			h.authKeys = append(h.authKeys, k)
		}
	case "authkey-check":
		return "PRESENT=" + b01(slices.Contains(h.authKeys, between(script, "KEY='", "'"))) + "\n", nil
	case "hub-key":
		return testHubPub + " heyearth-hub\n", nil
	case "socks-probe":
		if h.udpChoked || (h.probeDead != nil && h.probeDead(op)) {
			return "HTTP=000\n", nil
		}
		return "HTTP=204\n", nil
	case "vr-probe": // the probe config carries the tunnel UUID: the script removes it
		cfg := between(script, "VR_CFG='", "'")
		h.remove(cfg)
		if h.vrFail || (h.probeDead != nil && h.probeDead(op)) {
			return "[vr-probe] xray: connection refused\nHTTP=000\n", nil
		}
		return "HTTP=204\n", nil
	case "mtproxy-install":
		args := installMTProxyArgs(script)
		if h.mtproxyFail {
			return "", errors.New("exit status 1: ERR: fronting domain does not resolve to this node; secret: " + args[1])
		}
		h.active["mtproxy"], h.enabled["mtproxy"], h.mtSecret = true, true, args[1]
		return "[install-mtproxy] DONE\nsecret: " + args[1] + "\n", nil
	case "mtproxy-check":
		a := b01(h.active["mtproxy"])
		return "ACTIVE=" + a + "\nLISTEN=" + a + "\nSECRET=" + b01(h.mtSecret != "" && h.mtSecret == between(script, "S='", "'")) +
			"\nVERSION=" + h.mtgVersion + "\n", nil
	case "hub-client-remove":
		u := "hysteria2-client-" + f[1]
		h.remove("/etc/systemd/system/"+u+".service", config.HubClientConfigPath(atoi(f[1])))
		h.active[u], h.enabled[u] = false, false
		h.clearMarker(u)
		delete(h.pathMark, u)
	case "hub-client-state":
		u := "hysteria2-client-" + f[1]
		return "UNIT=" + b01(h.files["/etc/systemd/system/"+u+".service"] != nil) + "\nCONFIG=" + b01(h.files[config.HubClientConfigPath(atoi(f[1]))] != nil) +
			"\nACTIVE=" + b01(h.active[u]) + "\nLINK=" + b01(h.enabled[u]) + "\n", nil
	case "rotate-password":
		pw, line := between(script, "NEW='", "'"), between(script, "RP_LINE='", "'")
		if y, ok := h.files[config.ExitHysteriaConfig]; ok {
			h.files[config.ExitHysteriaConfig] = []byte(fakeYAMLPassword.ReplaceAllLiteralString(string(y), line))
		}
		if k, ok := h.files[config.ExitKeysPath]; ok {
			h.files[config.ExitKeysPath] = []byte(fakeJSONPassword.ReplaceAllLiteralString(string(k), `"ssPassword": "`+pw+`"`))
		}
		h.remove(config.ExitHysteriaConfig+".bak", config.ExitKeysPath+".bak")
	case "password-check":
		pw, line := between(script, "NEW='", "'"), between(script, "RP_LINE='", "'")
		y, k := "absent", "absent"
		if d, ok := h.files[config.ExitHysteriaConfig]; ok {
			y = "old"
			if strings.Contains(string(d), "\n"+line+"\n") {
				y = "new"
			}
		}
		if d, ok := h.files[config.ExitKeysPath]; ok {
			k = "none"
			if m := fakeJSONPassword.FindString(string(d)); m != "" {
				k = "old"
				if m == `"ssPassword": "`+pw+`"` {
					k = "new"
				}
			}
		}
		return "YAML=" + y + "\nKEYS=" + k + "\n", nil
	case "stop-services":
		for _, u := range config.ExitServiceUnits() {
			h.active[u], h.enabled[u] = false, false
		}
	case "remove-guard":
		if h.active["xray"] {
			return "", errors.New("exit status 1: [exit] xray ещё работает — защиту исходящих не снимаю")
		}
		h.remove(config.ExitXrayGuardConfPath, config.ExitXrayGuardUnitPath)
		h.nft[config.ExitXrayGuardTable] = false
		h.active[config.ExitXrayGuardService], h.enabled[config.ExitXrayGuardService] = false, false
	case "remove-hop":
		h.remove(config.ExitPortHopConfPath, config.ExitPortHopUnitPath)
		h.nft[config.ExitPortHopTable] = false
		h.active[config.ExitPortHopService], h.enabled[config.ExitPortHopService] = false, false
	case "teardown-state":
		var b strings.Builder
		for _, u := range append(config.ExitServiceUnits(), config.ExitXrayGuardService, config.ExitPortHopService) {
			fmt.Fprintf(&b, "ACTIVE_%s=%s\nENABLED_%s=%s\n", u, b01(h.active[u]), u, b01(h.enabled[u]))
		}
		fmt.Fprintf(&b, "GUARD_TABLE=%s\nGUARD_FILES=%s\nHOP_TABLE=%s\nHOP_FILES=%s\n",
			b01(h.nft[config.ExitXrayGuardTable]), b01(h.files[config.ExitXrayGuardConfPath] != nil || h.files[config.ExitXrayGuardUnitPath] != nil),
			b01(h.nft[config.ExitPortHopTable]), b01(h.files[config.ExitPortHopConfPath] != nil || h.files[config.ExitPortHopUnitPath] != nil))
		return b.String(), nil
	}
	return "", nil // other ops (firewall, base, hop-apply, …) succeed without a modelled effect
}

func atoi(s string) int {
	n := 0
	fmt.Sscanf(s, "%d", &n)
	return n
}
