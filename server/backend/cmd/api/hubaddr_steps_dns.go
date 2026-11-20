package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/heyearth/backend/internal/hubnet"
)

// Step 4 «DNS» (by hand): the wizard resolves the names that may point at the Hub, remembers what
// they returned the first time (those on the current address are the owner's to change), and opens
// the gate once every such name returns only the new address and the site's certificate verifies
// on it for that name.

type hubAddrDNSChecks struct {
	Before    map[string]string `json:"before"`
	Rows      []hubnet.DNSRow   `json:"rows"`
	Answered  int               `json:"answered"`
	Certs     []hubAddrProbe    `json:"certs"`
	Passed    bool              `json:"passed"`
	Trusted   bool              `json:"trusted"` // the owner moved on on their word
	TrustedBy string            `json:"trusted_by"`
}

// hubDNSNames: the names to look at, de-duplicated, blanks skipped.
func (e *hubAddrEngine) hubDNSNames() []string {
	names := []string{"hiearth.ru", "www.hiearth.ru", "admin.hiearth.ru", "site.hiearth.ru", "cdn.hiearth.ru", "tg.hiearth.ru"}
	if v, _ := e.env.Setting("site_url", ""); v != "" {
		if u, err := url.Parse(strings.TrimSpace(v)); err == nil {
			names = append(names, u.Hostname())
		}
	}
	for _, k := range []string{"hub_main_server_host", "sub_domain", "tg_webproxy_host"} {
		if v, _ := e.env.Setting(k, ""); v != "" {
			names = append(names, v)
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		n = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(n)), ".")
		if n == "" || seen[n] || mustAddr(n).IsValid() {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (e *hubAddrEngine) stepDNS(ctx context.Context, c hubAddrChange, prev hubAddrStep, lg *stepLog) (any, error) {
	old, nw := mustAddr(c.OldIP), mustAddr(c.NewIP)
	var ch hubAddrDNSChecks
	if len(prev.Checks) > 0 {
		_ = json.Unmarshal(prev.Checks, &ch)
	}
	names := e.hubDNSNames()
	if ch.Before == nil {
		ch.Before = map[string]string{}
	}
	for _, n := range names {
		if _, ok := ch.Before[n]; ok {
			continue
		}
		for _, s := range hubnet.DNSServers {
			if as, err := e.env.Resolve(ctx, s, n); err == nil && len(as) > 0 {
				ch.Before[n] = as[0].Unmap().String()
				break
			}
		}
	}
	ch.Rows, ch.Answered = hubnet.CheckDNS(ctx, e.env.Resolve, names, hubnet.DNSServers, old, nw, ch.Before)
	gate := hubnet.DNSGate(ch.Rows, ch.Answered)
	ch.Certs = nil
	var certBad []string
	if gate == nil {
		for _, r := range ch.Rows {
			if !r.PointsAtOld {
				continue
			}
			err := e.env.CertCheck(ctx, nw.String(), r.Name)
			ch.Certs = append(ch.Certs, hubAddrProbe{Name: r.Name, OK: err == nil, Error: errTextOrEmpty(err)})
			if err != nil {
				certBad = append(certBad, r.Name+": "+err.Error())
			}
		}
	}
	for _, r := range ch.Rows {
		lg.printf("%s: было %s, сейчас %v — %s", r.Name, r.Before, r.ByServer, r.Note)
	}
	if gate == nil && len(certBad) > 0 {
		gate = fmt.Errorf("сертификат не подходит на новом адресе: %s", strings.Join(certBad, "; "))
	}
	if gate != nil {
		if cur, err := e.env.Repo.Get(ctx, c.ID); err == nil && cur.State == hubAddrDNSReady {
			_ = e.env.Repo.SetState(ctx, c.ID, hubAddrDNSReady, hubAddrNodesReady, nil)
		}
		return ch, gate
	}
	ch.Passed = true
	lg.printf("DNS сошёлся на %s", nw)
	return ch, e.advance(ctx, c, hubAddrDNSReady, nil)
}

// TrustDNS: the owner says the records are changed — move on without waiting for the resolvers'
// caches (the old address stays on the Hub until step 7, so a stale answer still reaches the site).
func (e *hubAddrEngine) TrustDNS(ctx context.Context, id int64, by string) error {
	c, err := e.env.Repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if c.State == hubAddrDNSReady {
		return nil
	}
	if c.State != hubAddrNodesReady {
		return fmt.Errorf("DNS подтверждают после шага «Подготовить узлы», сейчас «%s»", c.State)
	}
	e.mu.Lock()
	running := e.busy[id] != ""
	e.mu.Unlock()
	if running {
		return errHubAddrBusy
	}
	var ch hubAddrDNSChecks
	if s, ok := c.Steps[hubStepDNS]; ok && len(s.Checks) > 0 {
		_ = json.Unmarshal(s.Checks, &ch)
	}
	ch.Trusted, ch.TrustedBy, ch.Passed = true, by, true
	b, _ := json.Marshal(ch)
	now := e.env.Now().UTC()
	if err := e.env.Repo.SaveStep(ctx, id, hubStepDNS, hubAddrStep{Status: hubStepOK, FinishedAt: &now, Checks: b,
		Log: "DNS подтверждён владельцем (" + by + "): кеши резолверов не ждали"}); err != nil {
		return err
	}
	return e.env.Repo.SetState(ctx, id, hubAddrNodesReady, hubAddrDNSReady, nil)
}
