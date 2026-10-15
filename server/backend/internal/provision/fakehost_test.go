package provision

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
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
	files       map[string][]byte
	modes       map[string]uint32
	writes      int
	active      map[string]bool
	enabled     map[string]bool
	retry       map[string]bool // in the pause of a crash loop (SubState auto-restart)
	nft         map[string]bool // nft tables (inet) loaded in the kernel
	marker      map[string]bool
	markWrites  map[string]map[string]string // unit → path → sha256 recorded by MarkRestartScript
	restarts    map[string]int
	authKeys    []string
	ops         []string
	scripts     map[string]string // op → the last script run with it
	seen        func(op string)   // called for every op before it runs (tests watch the routing state)
	dropOnce    string            // the first op with this prefix loses the SSH connection…
	dropped     bool
	down        bool // …and everything after it fails too (no rollback) until the next job dials again
	mtproxyFail bool
	mtSecret    string // the secret mtproxy runs with (set by mtproxy-install)
	mtgVersion  string
	// Hub: UDP to the exits is choked — a hysteria client crash-loops after every (re)start (the
	// real client exits when its first connection fails) and nothing answers through its SOCKS.
	udpChoked bool
	vrFail    bool // Hub: the TCP path probe gets no 204
	// dropAfterPut: the first write of one of these paths lands, then the connection is lost.
	dropAfterPut []string
}

// errLost is what a dropped SSH session returns (DialNode's SSHRemote wraps ErrConnectionLost).
var errLost = fmt.Errorf("%w: ssh: connection lost", ErrConnectionLost)

func newFakeHost() *fakeHost {
	return &fakeHost{files: map[string][]byte{}, modes: map[string]uint32{}, active: map[string]bool{}, enabled: map[string]bool{},
		retry: map[string]bool{}, nft: map[string]bool{}, marker: map[string]bool{}, markWrites: map[string]map[string]string{},
		restarts: map[string]int{}, scripts: map[string]string{}, mtgVersion: "2.2.8"}
}

var fakeMarkLine = regexp.MustCompile(`(?m)^l='([0-9a-f]{64})  ([^']*)'`)

// converge models UnitConvergeScript / UnitConvergeSoftScript: the extra loads the unit's guard
// (xray: the egress guard, node-agent: the Hub-only guard) when its file is there; the unit is
// restarted when marked or down. A Hub client under udpChoked crash-loops after the restart: the
// strict converge fails with the marker kept, the soft one reports HEALTH=0 and drops it.
func (h *fakeHost) converge(u string, soft bool) (string, error) {
	switch u {
	case "xray":
		if h.files[config.ExitXrayGuardConfPath] != nil {
			h.nft[config.ExitXrayGuardTable] = true
			h.enabled[config.ExitXrayGuardService] = true
		}
	case "node-agent":
		if h.files[config.ExitAgentGuardConfPath] != nil {
			h.nft[config.ExitAgentGuardTable] = true
			h.active[config.ExitAgentGuardService], h.enabled[config.ExitAgentGuardService] = true, true
		}
	}
	h.enabled[u] = true
	if !h.marker[u] && h.active[u] {
		return "[unit] " + u + ": без изменений и работает — без перезапуска\n", nil
	}
	h.restarts[u]++
	if h.udpChoked && strings.HasPrefix(u, "hysteria2-client-") {
		h.active[u], h.retry[u] = false, true
		if !soft {
			h.marker[u] = true
			return "", errors.New("exit status 1: [unit] " + u + " перезапускается сам (NRestarts 1 -> 2)")
		}
		delete(h.marker, u)
		return "HEALTH=0\n", nil
	}
	h.active[u], h.retry[u] = true, false
	delete(h.marker, u)
	if soft {
		return "HEALTH=1\n", nil
	}
	return "", nil
}

func (h *fakeHost) PutFile(_ context.Context, path string, data []byte, mode uint32) (bool, error) {
	h.ops = append(h.ops, "put "+path)
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
	h.ops = append(h.ops, op)
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
			if h.markWrites[f[1]] == nil {
				h.markWrites[f[1]] = map[string]string{}
			}
			h.markWrites[f[1]][m[2]] = m[1]
		}
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
			"\nMARKER=" + b01(h.marker[f[1]]) + "\n", nil
	case "rollback":
		h.active[f[1]] = false
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
		if h.udpChoked {
			return "HTTP=000\n", nil
		}
		return "HTTP=204\n", nil
	case "vr-probe": // the probe config carries the tunnel UUID: the script removes it
		cfg := between(script, "VR_CFG='", "'")
		h.remove(cfg)
		if h.vrFail {
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
