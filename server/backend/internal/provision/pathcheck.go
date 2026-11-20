package provision

// «Проверить путь» (KindPathCheck) and «Проверить туннель» (KindTunnelCheck) of a reserve entry
// (docs/prd/entries-management.prd.md): the per-flow check behind returning an entry to rotation
// — deploy --path-matrix from the admin panel — and a trial GRE tunnel before turning it on.
//
// pathcheck changes nothing: hub → entry and entry → hub TCP connects from many fixed source
// ports (between the tunnel addresses for an entry on the tunnel, the public path then BACKUP and
// not judged), and what users get — TLS and Bamboo through an exit abroad (RelayFlowProbe). The
// probes only record; the last step judges every group (PathVerdict).
//
// tunnelcheck brings up both ends of the entry's tunnel (the Hub's with its guard left armed —
// it removes the tunnel within HubTunnelGuardSeconds even if the API dies), measures the Hub
// through it from many source ports, and takes both ends down again. The entry's rules and
// nginx are never touched: users keep the public path throughout.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/shellquote"
)

// Floors of the groups (percent of the flows that must succeed) and the flows per group.
const (
	PathRawPct  = 95 // a raw 5-tuple group below this has a broken link on the way
	PathUserPct = 99 // what users get through the entry (TLS via an exit)
	PathUDPPct  = 95 // Bamboo through the entry (UDP loses a probe now and then)
	PathTuples  = 50
)

// PathProbePy: TCP connects from n fixed source ports; prints "<name> <ok>/<n> <pattern>".
const PathProbePy = `import socket, struct, sys, random
name, host, port, n = sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4])
base = random.randint(20000, 60000 - n)
out = ""
for sp in range(base, base + n):
    s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, struct.pack("ii", 1, 0)); s.settimeout(2)
    try:
        s.bind(("", sp)); s.connect((host, port)); out += "+"
    except Exception:
        out += "-"
    s.close()
print("%s %d/%d %s" % (name, out.count("+"), n, out))
`

// PathVerdict reads RAW / USER / UDP lines ("<kind> <name> <ok>/<n> …"): every group must reach
// its floor, and the log must have as many groups as its "EXPECT n" line says (7 without one —
// deploy --path-matrix before the tunnel). BACKUP lines are shown, never judged.
func PathVerdict(log string) (pass bool, failed []string) {
	seen, expect := 0, 7
	for _, l := range strings.Split(log, "\n") {
		f := strings.Fields(l)
		if len(f) == 2 && f[0] == "EXPECT" {
			if n, err := strconv.Atoi(f[1]); err == nil && n > 0 {
				expect = n
			}
			continue
		}
		floor := map[string]int{"RAW": PathRawPct, "USER": PathUserPct, "UDP": PathUDPPct}
		if len(f) < 3 || floor[f[0]] == 0 {
			continue
		}
		ok, n, found := strings.Cut(f[2], "/")
		a, err1 := strconv.Atoi(ok)
		b, err2 := strconv.Atoi(n)
		if !found || err1 != nil || err2 != nil || b == 0 {
			failed = append(failed, f[1]+" (не разобрано)")
			continue
		}
		seen++
		if 100*a < floor[f[0]]*b {
			failed = append(failed, fmt.Sprintf("%s %d/%d < %d%%", f[1], a, b, floor[f[0]]))
		}
	}
	if seen != expect {
		failed = append(failed, fmt.Sprintf("групп %d из %d", seen, expect))
	}
	return len(failed) == 0, failed
}

// FlowCounts: successes out of N flows through an exit abroad to the entry — TLS with the Reality
// SNI to :443 and :2443, Bamboo VN probes through random hop ports (UDP < 0: Bamboo is not
// deployed, no group).
type FlowCounts struct{ TLS443, TLS2443, UDP, N int }

// RelayFlowProbe runs n flows of each kind through the first working exit (cmd/api wires it, as
// RelayPathVerifier). An error means the probe could not run (no exit, a broken SOCKS): the step
// fails, it is not a verdict.
var RelayFlowProbe func(ctx context.Context, relayIP string, n int) (FlowCounts, error)

// pathExpect: the groups of a pathcheck — hub → entry (2 public, or 1 through the tunnel), entry →
// hub (3), via exit (TLS 443 and 2443, Bamboo when deployed).
func pathExpect(tunnel, udp bool) int {
	n := 2 + 3 + 2
	if tunnel {
		n = 1 + 3 + 2
	}
	if udp {
		n++
	}
	return n
}

// pathLog collects the group lines of one check run (the verdict step reads them).
type pathLog struct{ b strings.Builder }

func (p *pathLog) add(s *State, out string) {
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) >= 3 && (f[0] == "RAW" || f[0] == "USER" || f[0] == "UDP" || f[0] == "BACKUP") {
			p.b.WriteString(strings.TrimSpace(l) + "\n")
			s.Printf("%s", strings.TrimSpace(l))
		}
	}
}

// probeSh writes PathProbePy to a temp file and runs each "<kind> <name> <host> <port>" group.
func probeSh(groups [][4]string) string {
	py := base64.StdEncoding.EncodeToString([]byte(PathProbePy))
	var b strings.Builder
	b.WriteString(`P=$(mktemp); trap 'rm -f "$P"' EXIT; printf '%s' ` + shellquote.Quote(py) + ` | base64 -d > "$P"` + "\n")
	for _, g := range groups {
		fmt.Fprintf(&b, "echo \"%s $(python3 \"$P\" %s %s %s %d)\"\n", g[0], shellquote.Quote(g[1]), shellquote.Quote(g[2]), g[3], PathTuples)
	}
	return b.String()
}

// pathHubGroups / pathEntryGroups: what each end measures (PUB=BACKUP for an entry on the tunnel).
func pathHubGroups(o RelayOptions) [][4]string {
	if !o.Tunnel {
		return [][4]string{{"RAW", "hub→entry:443", o.RelayIP, "443"}, {"RAW", "hub→entry:2443", o.RelayIP, "2443"}}
	}
	return [][4]string{{"BACKUP", "hub→entry:443", o.RelayIP, "443"}, {"BACKUP", "hub→entry:2443", o.RelayIP, "2443"},
		{"RAW", "tunnel-hub→entry:443", str(o.tunnel().relay), "443"}}
}

func pathEntryGroups(o RelayOptions) [][4]string {
	pub := "RAW"
	if o.Tunnel {
		pub = "BACKUP"
	}
	var g [][4]string
	for _, p := range []string{strconv.Itoa(config.RelayHubPPPort), "443", "2443"} {
		g = append(g, [4]string{pub, "entry→hub:" + p, o.HubIP, p})
	}
	if o.Tunnel {
		for _, p := range []string{strconv.Itoa(config.RelayHubPPPort), "443", "2443"} {
			g = append(g, [4]string{"RAW", "tunnel-entry→hub:" + p, str(o.tunnel().hub), p})
		}
	}
	return g
}

func checkEntry(n Node, st *State) (RelayOptions, error) {
	if n.Role != "relay" {
		return RelayOptions{}, errors.New("проверка пути — только для входа")
	}
	if n.Lifecycle != "online" {
		return RelayOptions{}, fmt.Errorf("проверка — только для настроенного входа, а он «%s»", n.Lifecycle)
	}
	o := st.Relay
	if o.RelayIP == "" {
		return RelayOptions{}, errors.New("параметры входа не заданы: API не вызвал RelayOptionsFor")
	}
	if err := o.validate(); err != nil {
		return RelayOptions{}, err
	}
	if o.ServerID != n.ID {
		return RelayOptions{}, fmt.Errorf("параметры входа собраны для сервера %d, а это сервер %d", o.ServerID, n.ID)
	}
	return o, nil
}

// pathCheckRecipe: three probe steps that only record (a failed probe tool fails its step), then
// the verdict.
func pathCheckRecipe(n Node, st *State, l relayLayout) (Recipe, error) {
	o, err := checkEntry(n, st)
	if err != nil {
		return nil, err
	}
	pl := &pathLog{}
	udp := true
	return Recipe{
		{Name: "path_hub_to_entry", Verify: func(ctx context.Context, _ Remote, s *State) error {
			if s.Hub == nil {
				return errors.New("нет доступа к Hub")
			}
			out, err := s.Hub.Run(ctx, "# hp:path-hub\nset -u\n"+probeSh(pathHubGroups(o)))
			pl.add(s, out)
			return err
		}},
		{Name: "path_entry_to_hub", Verify: func(ctx context.Context, r Remote, s *State) error {
			out, err := r.Run(ctx, l.script("path-entry", o, probeSh(pathEntryGroups(o))))
			pl.add(s, out)
			return err
		}},
		{Name: "path_via_exit", Verify: func(ctx context.Context, _ Remote, s *State) error {
			if RelayFlowProbe == nil {
				return fmt.Errorf("%w: API не подключил замер через выход", ErrProbeUnavailable)
			}
			c, err := RelayFlowProbe(ctx, o.RelayIP, PathTuples)
			if err != nil {
				return fmt.Errorf("замер через выход не выполнился: %w", err)
			}
			out := fmt.Sprintf("USER via-exit:443 %d/%d\nUSER via-exit:2443 %d/%d\n", c.TLS443, c.N, c.TLS2443, c.N)
			if c.UDP >= 0 {
				out += fmt.Sprintf("UDP via-exit:bamboo %d/%d\n", c.UDP, c.N)
			} else {
				udp = false
			}
			pl.add(s, out)
			return nil
		}},
		{Name: "path_verdict", Verify: func(_ context.Context, _ Remote, s *State) error {
			pass, failed := PathVerdict(fmt.Sprintf("EXPECT %d\n%s", pathExpect(o.Tunnel, udp), pl.b.String()))
			if !pass {
				return fmt.Errorf("путь не готов быть обязательным: %s", strings.Join(failed, "; "))
			}
			s.Printf("PATH_MATRIX verdict=PASS")
			return nil
		}},
	}, nil
}

// relayHubTunnelTry is the Hub step of tunnelcheck: up with the guard left armed.
var relayHubTunnelTry = func(ctx context.Context, hub Remote, s *State, o RelayOptions) error {
	return hubTunnelPaths.upGuarded(ctx, hub, s, o, true)
}

// relayTunnelUfwSh lets GRE from the Hub in when ufw is on (what the tunnel recipe's firewall
// step allows; harmless for an entry that never uses it).
const relayTunnelUfwSh = relayUfwOn + `if ufw_on; then ufw allow proto gre from "$HUB" to "$RELAY" comment heyearth-relay >/dev/null; echo "ufw: GRE от Hub $HUB разрешён"; else echo "ufw выключен"; fi
`

const relayTunnelUfwCheckSh = relayUfwOn + `if ! ufw_on; then exit 0; fi
st=$(LC_ALL=C ufw status 2>/dev/null) || st=""
awk -v h="$HUB" 'index($0, "gre") && index($0, "ALLOW") && index($0, h) { f = 1 } END { exit !f }' <<<"$st" || echo "RELAY_FAIL ufw: GRE от Hub $HUB не разрешён"
`

// tunnelCheckRecipe: for an entry without the tunnel only.
func tunnelCheckRecipe(n Node, st *State, l relayLayout) (Recipe, error) {
	o, err := checkEntry(n, st)
	if err != nil {
		return nil, err
	}
	if n.RelayTunnel || o.Tunnel {
		return nil, errors.New("вход уже на туннеле: путь через туннель проверяет «Проверить путь»")
	}
	ot := o
	ot.Tunnel = true
	pl := &pathLog{}
	upErr := ""
	rc := Recipe{
		// Only what the tunnel needs: the module, a free /30 here, the Hub reachable (the entry
		// already serves users, so its ports are taken — not a failure here).
		{Name: "tunnel_preflight", Verify: func(ctx context.Context, r Remote, s *State) error {
			cs, err := l.checks(ctx, r, ot)
			if err != nil {
				return err
			}
			var bad []string
			for _, c := range cs {
				if c.Name == "Туннель до Hub" || c.Name == "Связь с Hub" {
					s.Printf("%s %s %s", map[bool]string{true: "✓", false: "✗"}[c.OK], c.Name, c.Detail)
					if !c.OK {
						bad = append(bad, c.Name+": "+c.Detail)
					}
				}
			}
			if len(bad) > 0 {
				return fmt.Errorf("туннель здесь не поднять: %s", strings.Join(bad, "; "))
			}
			return nil
		}},
		{Name: "tunnel_relay_firewall", Apply: func(ctx context.Context, r Remote, s *State) error {
			return l.run(ctx, r, s, "tunnel-ufw", ot, relayTunnelUfwSh)
		}, Verify: func(ctx context.Context, r Remote, _ *State) error {
			return l.check(ctx, r, "tunnel-ufw-check", ot, relayTunnelUfwCheckSh)
		}},
		{Name: "relay_hub_tunnel", Apply: func(ctx context.Context, _ Remote, s *State) error {
			return relayHubTunnelTry(ctx, s.Hub, s, ot)
		}, Verify: func(ctx context.Context, _ Remote, s *State) error {
			return relayHubTunnelCheck(ctx, s.Hub, ot)
		}, Rollback: func(ctx context.Context, _ Remote, s *State) error {
			return relayHubTunnelRollback(ctx, s.Hub, s, ot)
		}},
		// The relay's end: a failure is recorded (the verdict names it) and the new unit removed —
		// the job goes on to take the Hub's end down.
		{Name: "tunnel_relay_up", Apply: func(ctx context.Context, r Remote, s *State) error {
			if err := l.tunnelApply(ot)(ctx, r, s); err != nil {
				upErr = err.Error()
				s.Printf("туннель на входе не поднялся: %v", err)
				if rerr := l.tunnelRollback(ot)(ctx, r, s); rerr != nil {
					s.Printf("откат туннеля на входе: %v", rerr)
				}
			}
			return nil
		}},
		{Name: "tunnel_flows", Apply: func(ctx context.Context, r Remote, s *State) error {
			if upErr != "" {
				s.Printf("замер пропущен: туннель не поднялся")
				return nil
			}
			var g [][4]string
			for _, p := range []string{strconv.Itoa(config.RelayHubPPPort), "443", "2443"} {
				g = append(g, [4]string{"RAW", "tunnel-entry→hub:" + p, str(ot.tunnel().hub), p})
			}
			out, err := r.Run(ctx, l.script("tunnel-flows", ot, probeSh(g)))
			pl.add(s, out)
			if err != nil {
				upErr = "замер не выполнился: " + err.Error()
				s.Printf("%s", upErr)
			}
			return nil
		}},
	}
	rc = append(rc, l.tunnelRemoveSteps(o)...)
	return append(rc, Step{Name: "tunnel_verdict", Verify: func(_ context.Context, _ Remote, s *State) error {
		if upErr != "" {
			return fmt.Errorf("туннель не работает: %s", upErr)
		}
		pass, failed := PathVerdict("EXPECT 3\n" + pl.b.String())
		if !pass {
			return fmt.Errorf("через туннель теряются потоки: %s", strings.Join(failed, "; "))
		}
		s.Printf("TUNNEL_CHECK verdict=PASS: туннель снят, его можно включать")
		return nil
	}}), nil
}
