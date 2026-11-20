package hubnet

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
	"time"
)

// Resolvers the DNS step asks (at least two must answer).
var DNSServers = []string{"1.1.1.1", "8.8.8.8", "77.88.8.8"}

// Resolve asks one DNS server for the A records of name.
type Resolve func(ctx context.Context, server, name string) ([]netip.Addr, error)

// GoResolve asks server directly (UDP 53, 3 s), bypassing the Hub's own resolver.
func GoResolve(ctx context.Context, server, name string) ([]netip.Addr, error) {
	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		d := net.Dialer{Timeout: 3 * time.Second}
		return d.DialContext(ctx, "udp", net.JoinHostPort(server, "53"))
	}}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return r.LookupNetIP(ctx, "ip4", name)
}

// DNSRow is one name of the DNS step.
type DNSRow struct {
	Name        string              `json:"name"`
	ByServer    map[string][]string `json:"by_server"` // server → answers ("" key absent = no answer)
	Before      string              `json:"before"`    // what it resolved to when the step first ran
	PointsAtOld bool                `json:"points_at_old"`
	Converged   bool                `json:"converged"` // every answering server returns only the new address
	Pending     []string            `json:"pending"`   // answering servers that do not return only the new address
	Note        string              `json:"note"`
}

// CheckDNS resolves names on servers. before holds the first answers of the step (name →
// address); a name that pointed at old then is one the owner has to change. answered = servers
// that answered at least one name.
func CheckDNS(ctx context.Context, resolve Resolve, names, servers []string, old, new netip.Addr, before map[string]string) ([]DNSRow, int) {
	answered := map[string]bool{}
	var rows []DNSRow
	for _, name := range names {
		row := DNSRow{Name: name, ByServer: map[string][]string{}, Before: before[name]}
		row.PointsAtOld = row.Before == old.String()
		conv := true
		for _, s := range servers {
			as, err := resolve(ctx, s, name)
			if err != nil {
				continue
			}
			answered[s] = true
			var strs []string
			onlyNew := len(as) > 0
			for _, a := range as {
				strs = append(strs, a.Unmap().String())
				if a.Unmap() != new {
					onlyNew = false
				}
			}
			sort.Strings(strs)
			row.ByServer[s] = strs
			if !onlyNew {
				conv = false
				row.Pending = append(row.Pending, s)
			}
		}
		row.Converged = conv && len(row.ByServer) > 0
		switch {
		case row.PointsAtOld && !row.Converged:
			row.Note = fmt.Sprintf("поменяйте A-запись %s на %s (оранжевое облако не включать)", name, new)
		case !row.PointsAtOld:
			row.Note = "указывает не на хаб — не трогаем"
		}
		rows = append(rows, row)
	}
	return rows, len(answered)
}

// DNSGate: nil when at least two servers answered and every name that pointed at the old address
// now resolves only to the new one on every answering server.
func DNSGate(rows []DNSRow, answered int) error {
	if answered < 2 {
		return fmt.Errorf("ответили %d DNS-сервера из %d — нужно хотя бы два", answered, len(DNSServers))
	}
	var bad []string
	for _, r := range rows {
		if !r.PointsAtOld || r.Converged {
			continue
		}
		why := strings.Join(r.Pending, ", ")
		if why == "" {
			why = "никто не ответил"
		}
		bad = append(bad, fmt.Sprintf("%s (ещё не новый адрес: %s)", r.Name, why))
	}
	if len(bad) > 0 {
		return fmt.Errorf("DNS ещё не сошёлся: %s", strings.Join(bad, "; "))
	}
	return nil
}
