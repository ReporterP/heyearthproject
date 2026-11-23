// Package synth is the synthetic client: it takes the JSON subscription exactly as the app
// gets it, runs a real xray core as a client and opens real targets through every line and
// every way of it. Running on the Hub, the client reaches the Hub and the entries from
// outside: its own outgoing connections go through the SOCKS of an exit (Via).
//
// config.go — pure transforms of a served config into what the core is started with;
// fetch.go — opening a target through the core; run.go — the bounded core process.
package synth

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
)

// Via is the SOCKS of an exit: every way dials through it, so the Hub's client looks at the
// Hub and the entries from abroad.
type Via struct {
	Host string
	Port int
}

// viaTag is the outbound a way's connection goes through.
const viaTag = "synth-via"

// Lines parses a JSON subscription body: an array of xray configs, one per line of the app's
// server list.
func Lines(body []byte) ([]map[string]any, error) {
	var lines []map[string]any
	if err := json.Unmarshal(body, &lines); err != nil {
		return nil, fmt.Errorf("not a JSON subscription: %w", err)
	}
	if len(lines) == 0 {
		return nil, errors.New("the subscription has no lines")
	}
	return lines, nil
}

// Label is the line's name in the app (remarks).
func Label(cfg map[string]any) string {
	s, _ := cfg["remarks"].(string)
	return s
}

// Ways lists the line's ways in: the tags of the outbounds its balancers select (by prefix),
// in the order they are served.
func Ways(cfg map[string]any) []string {
	var prefixes []string
	for _, b := range list(dig(cfg, "routing", "balancers")) {
		for _, s := range list(dig(b, "selector")) {
			if p, ok := s.(string); ok && p != "" {
				prefixes = append(prefixes, p)
			}
		}
	}
	var ways []string
	for _, o := range list(cfg["outbounds"]) {
		tag, _ := dig(o, "tag").(string)
		for _, p := range prefixes {
			if strings.HasPrefix(tag, p) {
				ways = append(ways, tag)
				break
			}
		}
	}
	return ways
}

// ForWay builds the config that sends everything through ONE way of the line: a SOCKS inbound
// on 127.0.0.1:socksPort, the way's outbound and a single rule to it — no balancer, no
// observatory, no dns. With via the way dials through the exit's SOCKS. cfg is not changed.
func ForWay(cfg map[string]any, way string, socksPort int, via *Via) (map[string]any, error) {
	if !contains(Ways(cfg), way) {
		return nil, fmt.Errorf("unknown way %q", way)
	}
	c, err := clone(cfg)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	for _, o := range list(c["outbounds"]) {
		if m, ok := o.(map[string]any); ok && m["tag"] == way {
			out = m
		}
	}
	outbounds := []any{out}
	if via != nil {
		throughVia(out)
		outbounds = append(outbounds, viaOutbound(via))
	}
	return map[string]any{
		"log":       map[string]any{"loglevel": "warning"},
		"inbounds":  []any{socksInbound("socks", socksPort, nil)},
		"outbounds": outbounds,
		"routing": map[string]any{"rules": []any{
			map[string]any{"type": "field", "network": "tcp,udp", "outboundTag": way},
		}},
	}, nil
}

// AsServed builds the config the app would run — the served rules, balancer, observatory and
// dns untouched — with one SOCKS inbound on 127.0.0.1:socksPort that keeps the served
// inbound's tag and sniffing (they decide where a name goes). With via every way dials through
// the exit's SOCKS. cfg is not changed.
func AsServed(cfg map[string]any, socksPort int, via *Via) (map[string]any, error) {
	c, err := clone(cfg)
	if err != nil {
		return nil, err
	}
	tag, sniffing := "socks", any(nil)
	for _, in := range list(c["inbounds"]) {
		if dig(in, "protocol") == "socks" {
			if t, ok := dig(in, "tag").(string); ok && t != "" {
				tag = t
			}
			sniffing = dig(in, "sniffing")
			break
		}
	}
	c["inbounds"] = []any{socksInbound(tag, socksPort, sniffing)}
	delete(c, "remarks") // the app's fields, not xray's
	delete(c, "meta")
	if via != nil {
		ways := Ways(c)
		outbounds := list(c["outbounds"])
		for _, o := range outbounds {
			if m, ok := o.(map[string]any); ok {
				if t, _ := m["tag"].(string); contains(ways, t) {
					throughVia(m)
				}
			}
		}
		c["outbounds"] = append(outbounds, viaOutbound(via))
	}
	return c, nil
}

// Lint checks a served config without running it. Each problem is one line.
func Lint(cfg map[string]any) []string {
	var problems []string
	// By name (IPOnDemand) every name on 80/443 is resolved before routing. Without a dns
	// section that is the device's resolver — the operator's DNS, which answers blocked names
	// with nothing or a stub (incident I-39). A vantage outside RU cannot see that happen, so
	// the shape of the config is checked instead.
	if dig(cfg, "routing", "domainStrategy") == "IPOnDemand" {
		if _, ok := cfg["dns"]; !ok {
			problems = append(problems, "by name without dns: routing.domainStrategy is IPOnDemand and the config has no dns section")
		}
	}
	if len(list(dig(cfg, "routing", "balancers"))) > 0 && len(Ways(cfg)) == 0 {
		problems = append(problems, "no ways: the balancer selects no outbound")
	}
	return problems
}

// pickPort chooses the single port a Bamboo way dials behind a proxy; tests replace it.
var pickPort = func(lo, hi int) int { return lo + rand.IntN(hi-lo+1) }

// throughVia makes a way dial through viaTag. A Bamboo (hysteria) way loses its port hopping:
// xray refuses udphop behind a proxy ("udphop requires being at the outermost level"). It
// dials one port picked from the hop range instead, which also checks the hop DNAT.
func throughVia(out map[string]any) {
	ss, ok := out["streamSettings"].(map[string]any)
	if !ok {
		ss = map[string]any{}
		out["streamSettings"] = ss
	}
	sockopt, ok := ss["sockopt"].(map[string]any)
	if !ok {
		sockopt = map[string]any{}
		ss["sockopt"] = sockopt
	}
	sockopt["dialerProxy"] = viaTag

	qp, ok := dig(ss, "finalmask", "quicParams").(map[string]any)
	if !ok {
		return
	}
	hop, ok := qp["udpHop"].(map[string]any)
	if !ok {
		return
	}
	delete(qp, "udpHop")
	ports, _ := hop["ports"].(string)
	if lo, hi, ok := portRange(ports); ok {
		if settings, ok := out["settings"].(map[string]any); ok {
			settings["port"] = pickPort(lo, hi)
		}
	}
}

// portRange parses "30000-65000".
func portRange(s string) (lo, hi int, ok bool) {
	a, b, found := strings.Cut(strings.TrimSpace(s), "-")
	if !found {
		return 0, 0, false
	}
	lo, err1 := strconv.Atoi(strings.TrimSpace(a))
	hi, err2 := strconv.Atoi(strings.TrimSpace(b))
	if err1 != nil || err2 != nil || lo < 1 || hi > 65535 || lo > hi {
		return 0, 0, false
	}
	return lo, hi, true
}

func viaOutbound(via *Via) map[string]any {
	return map[string]any{"tag": viaTag, "protocol": "socks", "settings": map[string]any{
		"servers": []any{map[string]any{"address": via.Host, "port": via.Port}},
	}}
}

func socksInbound(tag string, port int, sniffing any) map[string]any {
	in := map[string]any{"tag": tag, "listen": "127.0.0.1", "port": port, "protocol": "socks",
		"settings": map[string]any{"auth": "noauth", "udp": true}}
	if sniffing != nil {
		in["sniffing"] = sniffing
	}
	return in
}

// clone is a deep copy through JSON: configs are plain JSON values.
func clone(cfg map[string]any) (map[string]any, error) {
	b, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var c map[string]any
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return c, nil
}

// dig walks nested JSON objects by keys; nil when a step is missing.
func dig(v any, keys ...string) any {
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
