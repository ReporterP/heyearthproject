// Package hubnet renders and checks what changing the Hub's public address needs: netplan
// edits, ip/route/timer scripts, the nft counter of the old address, the SSH allow for nodes,
// outbound/DNS/SMTP checks and the roles of the Hub's addresses. Everything here is pure or
// takes its side effects as parameters; cmd/api runs the scripts through provision.Remote.
package hubnet

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Netplan edits are textual: the file is parsed only to find the lines to touch (yaml.Node
// keeps line/column), and exactly one line is inserted, removed or changed — the hand-written
// file keeps its comments, quoting and layout byte for byte. Anything unexpected (no such
// interface, a flow-style list, several default routes) is refused: the caller stops the step.

var itemPrefixRe = regexp.MustCompile(`^(\s*-\s+)`)

func netplanRoot(doc []byte) (*yaml.Node, error) {
	var n yaml.Node
	if err := yaml.Unmarshal(doc, &n); err != nil {
		return nil, fmt.Errorf("netplan не разбирается как YAML: %w", err)
	}
	if n.Kind != yaml.DocumentNode || len(n.Content) != 1 || n.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("netplan: пустой файл или не словарь")
	}
	return n.Content[0], nil
}

func mapGet(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func netplanIface(doc []byte, iface string) (*yaml.Node, error) {
	root, err := netplanRoot(doc)
	if err != nil {
		return nil, err
	}
	eth := mapGet(mapGet(mapGet(root, "network"), "ethernets"), iface)
	if eth == nil || eth.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("в netplan нет network.ethernets.%s", iface)
	}
	return eth, nil
}

// blockSeq returns key's value under m when it is a non-empty block-style sequence.
func blockSeq(m *yaml.Node, key, iface string) (*yaml.Node, error) {
	s := mapGet(m, key)
	switch {
	case s == nil:
		return nil, fmt.Errorf("в netplan у %s нет %s", iface, key)
	case s.Kind != yaml.SequenceNode || s.Style&yaml.FlowStyle != 0:
		return nil, fmt.Errorf("netplan: %s.%s не блочный список «- …» — правьте файл вручную", iface, key)
	case len(s.Content) == 0:
		return nil, fmt.Errorf("netplan: %s.%s пуст", iface, key)
	}
	return s, nil
}

func splitLines(doc []byte) []string { return strings.SplitAfter(string(doc), "\n") }

func addrOf(v string) (netip.Addr, bool) {
	v = strings.TrimSpace(v)
	if p, err := netip.ParsePrefix(v); err == nil {
		return p.Addr(), true
	}
	if a, err := netip.ParseAddr(v); err == nil {
		return a, true
	}
	return netip.Addr{}, false
}

// NetplanAddAddress appends cidr to network.ethernets.<iface>.addresses. changed=false when the
// address (any prefix length) is already listed.
func NetplanAddAddress(doc []byte, iface, cidr string) ([]byte, bool, error) {
	p, err := netip.ParsePrefix(cidr)
	if err != nil {
		return nil, false, fmt.Errorf("адрес %q не вида IP/маска", cidr)
	}
	eth, err := netplanIface(doc, iface)
	if err != nil {
		return nil, false, err
	}
	addrs, err := blockSeq(eth, "addresses", iface)
	if err != nil {
		return nil, false, err
	}
	for _, it := range addrs.Content {
		if a, ok := addrOf(it.Value); ok && a == p.Addr() {
			return doc, false, nil
		}
	}
	lines := splitLines(doc)
	last := addrs.Content[len(addrs.Content)-1]
	at := last.Line - 1
	if at < 0 || at >= len(lines) {
		return nil, false, errors.New("netplan: не нашёл строку последнего адреса")
	}
	m := itemPrefixRe.FindString(lines[at])
	if m == "" {
		return nil, false, errors.New("netplan: последний адрес записан не строкой «- …»")
	}
	ins := m + p.String() + "\n"
	if !strings.HasSuffix(lines[at], "\n") {
		lines[at] += "\n"
	}
	out := strings.Join(append(lines[:at+1], append([]string{ins}, lines[at+1:]...)...), "")
	return checkAddrs([]byte(out), iface, p.Addr(), true)
}

// NetplanRemoveAddress removes the line listing cidr's address. changed=false when it is not
// listed; the last remaining address is never removed.
func NetplanRemoveAddress(doc []byte, iface, cidr string) ([]byte, bool, error) {
	target, ok := addrOf(cidr)
	if !ok {
		return nil, false, fmt.Errorf("адрес %q не IP", cidr)
	}
	eth, err := netplanIface(doc, iface)
	if err != nil {
		return nil, false, err
	}
	addrs, err := blockSeq(eth, "addresses", iface)
	if err != nil {
		return nil, false, err
	}
	for _, it := range addrs.Content {
		a, ok := addrOf(it.Value)
		if !ok || a != target {
			continue
		}
		if len(addrs.Content) == 1 {
			return nil, false, errors.New("netplan: это единственный адрес интерфейса — не удаляю")
		}
		lines := splitLines(doc)
		at := it.Line - 1
		re := regexp.MustCompile(`^\s*-\s+["']?` + regexp.QuoteMeta(it.Value) + `["']?\s*(#.*)?\n?$`)
		if at < 0 || at >= len(lines) || !re.MatchString(lines[at]) {
			return nil, false, fmt.Errorf("netplan: строка адреса %s не отдельная «- …» — правьте вручную", it.Value)
		}
		out := strings.Join(append(lines[:at:at], lines[at+1:]...), "")
		return checkAddrs([]byte(out), iface, target, false)
	}
	return doc, false, nil
}

// checkAddrs re-parses an edited file: still valid, and the address is listed (want) or not.
func checkAddrs(out []byte, iface string, a netip.Addr, want bool) ([]byte, bool, error) {
	eth, err := netplanIface(out, iface)
	if err != nil {
		return nil, false, fmt.Errorf("после правки netplan сломан: %w", err)
	}
	found := false
	if s := mapGet(eth, "addresses"); s != nil {
		for _, it := range s.Content {
			if x, ok := addrOf(it.Value); ok && x == a {
				found = true
			}
		}
	}
	if found != want {
		return nil, false, errors.New("после правки netplan список адресов не тот — правка отменена")
	}
	return out, true, nil
}

func isDefaultDst(v string) bool {
	v = strings.TrimSpace(v)
	return v == "default" || v == "0.0.0.0/0"
}

// NetplanSetDefaultFrom sets `from: <src>` on the interface's default route (to: default or
// 0.0.0.0/0): the value is replaced, or a line is added after the route's last key.
func NetplanSetDefaultFrom(doc []byte, iface string, src netip.Addr) ([]byte, bool, error) {
	if !src.Is4() {
		return nil, false, fmt.Errorf("источник %s не IPv4", src)
	}
	eth, err := netplanIface(doc, iface)
	if err != nil {
		return nil, false, err
	}
	routes, err := blockSeq(eth, "routes", iface)
	if err != nil {
		return nil, false, err
	}
	var def *yaml.Node
	for _, r := range routes.Content {
		if r.Kind == yaml.MappingNode && isDefaultDst(val(mapGet(r, "to"))) {
			if def != nil {
				return nil, false, errors.New("netplan: у интерфейса несколько маршрутов по умолчанию — правьте вручную")
			}
			def = r
		}
	}
	if def == nil || len(def.Content) < 2 || def.Style&yaml.FlowStyle != 0 {
		return nil, false, errors.New("netplan: нет блочного маршрута по умолчанию (to: default)")
	}
	lines := splitLines(doc)
	want := src.String()
	if from := mapGet(def, "from"); from != nil {
		if strings.TrimSpace(from.Value) == want {
			return doc, false, nil
		}
		at := from.Line - 1
		col := from.Column - 1
		if at < 0 || at >= len(lines) || col < 0 || col >= len(lines[at]) {
			return nil, false, errors.New("netplan: не нашёл значение from")
		}
		line := lines[at]
		i := strings.Index(line[col:], from.Value)
		if i < 0 {
			return nil, false, errors.New("netplan: значение from записано непривычно — правьте вручную")
		}
		lines[at] = line[:col+i] + want + line[col+i+len(from.Value):]
	} else {
		lastVal := def.Content[len(def.Content)-1]
		key := def.Content[0]
		at := lastVal.Line - 1
		if at < 0 || at >= len(lines) || key.Column < 1 {
			return nil, false, errors.New("netplan: не нашёл конец маршрута по умолчанию")
		}
		if !strings.HasSuffix(lines[at], "\n") {
			lines[at] += "\n"
		}
		ins := strings.Repeat(" ", key.Column-1) + "from: " + want + "\n"
		lines = append(lines[:at+1], append([]string{ins}, lines[at+1:]...)...)
	}
	out := []byte(strings.Join(lines, ""))
	eth2, err := netplanIface(out, iface)
	if err != nil {
		return nil, false, fmt.Errorf("после правки netplan сломан: %w", err)
	}
	ok := false
	if rs := mapGet(eth2, "routes"); rs != nil {
		for _, r := range rs.Content {
			if isDefaultDst(val(mapGet(r, "to"))) && val(mapGet(r, "from")) == want {
				ok = true
			}
		}
	}
	if !ok {
		return nil, false, errors.New("после правки netplan from не тот — правка отменена")
	}
	return out, true, nil
}

func val(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	return strings.TrimSpace(n.Value)
}
