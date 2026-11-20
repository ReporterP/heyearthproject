package hubnet

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// RouteShowScript, AddrShowScript: what the wizard reads from the Hub.
const RouteShowScript = "ip -j -4 route show default\n"

func AddrShowScript(iface string) string { return "ip -j -4 addr show dev " + q(iface) + "\n" }

// ParseIfaceAddrs reads `ip -j -4 addr show dev <iface>`: the interface's IPv4 addresses with
// their prefixes, in the kernel's order.
func ParseIfaceAddrs(ipJSON []byte) ([]netip.Prefix, error) {
	var ifs []struct {
		AddrInfo []struct {
			Family    string `json:"family"`
			Local     string `json:"local"`
			Prefixlen int    `json:"prefixlen"`
		} `json:"addr_info"`
	}
	if err := json.Unmarshal(ipJSON, &ifs); err != nil {
		return nil, fmt.Errorf("вывод ip addr не разобран: %w", err)
	}
	if len(ifs) != 1 {
		return nil, errors.New("интерфейс маршрута по умолчанию не найден")
	}
	var out []netip.Prefix
	for _, a := range ifs[0].AddrInfo {
		if a.Family != "inet" {
			continue
		}
		ip, err := netip.ParseAddr(a.Local)
		if err != nil {
			continue
		}
		if p, err := ip.Prefix(a.Prefixlen); err == nil {
			out = append(out, netip.PrefixFrom(ip, p.Bits()))
		}
	}
	return out, nil
}

// HasAddr says whether a is among ps.
func HasAddr(ps []netip.Prefix, a netip.Addr) bool {
	for _, p := range ps {
		if p.Addr() == a {
			return true
		}
	}
	return false
}

// NetplanFilesScript lists every /etc/netplan/*.yaml as "=== <path>" + its base64 on one line.
func NetplanFilesScript() string {
	return "for f in /etc/netplan/*.yaml; do [ -f \"$f\" ] || continue; printf '=== %s\\n' \"$f\"; base64 -w0 \"$f\"; echo; done\n"
}

// ParseNetplanFiles reads NetplanFilesScript's output.
func ParseNetplanFiles(out string) (map[string][]byte, error) {
	files := map[string][]byte{}
	lines := strings.Split(out, "\n")
	for i := 0; i < len(lines); i++ {
		path, ok := strings.CutPrefix(strings.TrimSpace(lines[i]), "=== ")
		if !ok {
			continue
		}
		if i+1 >= len(lines) {
			return nil, fmt.Errorf("netplan %s: нет содержимого", path)
		}
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[i+1]))
		if err != nil {
			return nil, fmt.Errorf("netplan %s не прочитан: %w", path, err)
		}
		files[path] = b
		i++
	}
	return files, nil
}

// PickNetplanFile is the one file that lists addresses for iface; several or none is refused.
func PickNetplanFile(files map[string][]byte, iface string) (string, error) {
	var hits []string
	for path, doc := range files {
		eth, err := netplanIface(doc, iface)
		if err == nil && mapGet(eth, "addresses") != nil {
			hits = append(hits, path)
		}
	}
	sort.Strings(hits)
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return "", fmt.Errorf("ни один файл /etc/netplan/*.yaml не задаёт адреса %s", iface)
	}
	return "", fmt.Errorf("адреса %s заданы в нескольких файлах (%s) — оставьте один", iface, strings.Join(hits, ", "))
}
