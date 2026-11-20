package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/hubnet"
)

// Step 2 «Проверить адрес» (gate to step 3): outbound from the new address vs the current one,
// the services that might allow-list the Hub's source, inbound from abroad (through an exit),
// from RU (from each entry over SSH, 256 KiB through the address) and from the owner's phone.

type hubAddrProbe struct {
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type hubAddrCheckResult struct {
	Outbound []hubnet.TargetResult `json:"outbound"`
	SMTP     string                `json:"smtp"`     // ok | skipped | the error
	YooKassa string                `json:"yookassa"` // ok | skipped | the error
	Abroad   []hubAddrProbe        `json:"abroad"`
	Russia   []hubAddrProbe        `json:"russia"`
	Phone    string                `json:"phone"` // ok | skipped | not_done
	Bamboo   string                `json:"bamboo"`
	Passed   bool                  `json:"passed"`
	Blockers []string              `json:"blockers"`
}

// hubAddrPhoneRecord is the «phone» step record: which address the page runs on, its link, the
// last results read.
type hubAddrPhoneRecord struct {
	Target  string                    `json:"target"` // new | current
	URL     string                    `json:"url"`
	Expires time.Time                 `json:"expires"`
	Results []admin.MobileCheckResult `json:"results"`
}

const hubSiteName = "hiearth.ru"

func (e *hubAddrEngine) onlineNodes(ctx context.Context) ([]hubAddrNode, error) {
	nodes, err := e.env.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	var out []hubAddrNode
	for _, n := range nodes {
		if n.Online {
			out = append(out, n)
		}
	}
	return out, nil
}

func (e *hubAddrEngine) outboundTargets(ctx context.Context, nodes []hubAddrNode) []string {
	var ts []string
	for _, n := range nodes {
		ts = append(ts, net.JoinHostPort(n.IP, "443"))
	}
	ts = append(ts, "acme-v02.api.letsencrypt.org:443", "github.com:443", "api.yookassa.ru:443")
	if on, _ := e.env.Setting("smtp_enabled", ""); strings.EqualFold(strings.TrimSpace(on), "true") {
		host, _ := e.env.Setting("smtp_host", "")
		port, _ := e.env.Setting("smtp_port", "587")
		if h := strings.TrimSpace(host); h != "" {
			ts = append(ts, net.JoinHostPort(h, strings.TrimSpace(port)))
		}
	}
	return ts
}

// outbound probes every target from both addresses, targets in parallel.
func (e *hubAddrEngine) outbound(ctx context.Context, cur, nw netip.Addr, targets []string) []hubnet.TargetResult {
	res := make([]hubnet.TargetResult, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(i int, t string) {
			defer wg.Done()
			c := hubnet.ProbeTCP(ctx, e.env.Bound(cur), t, e.env.Tries)
			n := hubnet.ProbeTCP(ctx, e.env.Bound(nw), t, e.env.Tries)
			res[i] = hubnet.TargetResult{Target: t, Cur: c, New: n, Tries: e.env.Tries, Verdict: hubnet.OutboundVerdict(c, n, e.env.Tries)}
		}(i, t)
	}
	wg.Wait()
	return res
}

func errText(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}

func (e *hubAddrEngine) stepCheck(ctx context.Context, c hubAddrChange, lg *stepLog) (any, error) {
	cur, nw := mustAddr(c.OldIP), mustAddr(c.NewIP)
	var r hubAddrCheckResult
	nodes, err := e.onlineNodes(ctx)
	if err != nil {
		return r, err
	}
	blob := make([]byte, hubProbeSize)
	_, _ = rand.Read(blob)
	if _, err := e.env.Hub.PutFile(ctx, hubProbePath, blob, 0o644); err != nil {
		return r, fmt.Errorf("пробный файл не записан: %w", err)
	}

	r.Outbound = e.outbound(ctx, cur, nw, e.outboundTargets(ctx, nodes))
	for _, t := range r.Outbound {
		lg.printf("наружу %s: текущий %d/%d, новый %d/%d — %s", t.Target, t.Cur, t.Tries, t.New, t.Tries, t.Verdict)
		if t.Verdict == hubnet.VerdictFail {
			r.Blockers = append(r.Blockers, fmt.Sprintf("с нового адреса не доходит до %s (%d из %d против %d у текущего)", t.Target, t.New, t.Tries, t.Cur))
		}
	}
	r.SMTP, r.YooKassa = "skipped", "skipped"
	if e.env.SMTPAuth != nil {
		r.SMTP = serviceVerdict(e.env.SMTPAuth(ctx, e.env.Bound(nw)))
		if r.SMTP != "ok" && r.SMTP != "skipped" {
			r.Blockers = append(r.Blockers, "почта не принимает вход с нового адреса: "+r.SMTP)
		}
	}
	if e.env.YooKassa != nil {
		r.YooKassa = serviceVerdict(e.env.YooKassa(ctx, e.env.Bound(nw)))
		if r.YooKassa != "ok" && r.YooKassa != "skipped" {
			r.Blockers = append(r.Blockers, "ЮKassa не отвечает с нового адреса: "+r.YooKassa)
		}
	}

	if socks, err := e.env.PickExit(); err != nil {
		r.Blockers = append(r.Blockers, "нет живого выхода для проверки из-за рубежа: "+err.Error())
	} else {
		siteName, _ := e.env.Setting("hub_main_server_host", hubSiteName)
		for _, p := range []struct {
			name string
			fn   func() error
		}{
			{"сайт (" + siteName + ") на :443", func() error { return e.env.SiteTLS(socks, nw.String(), siteName) }},
			{"Берёза на :443", func() error { return e.env.RealityTLS(socks, nw.String(), 443) }},
			{"Дуб на :2443", func() error { return e.env.RealityTLS(socks, nw.String(), 2443) }},
		} {
			err := p.fn()
			r.Abroad = append(r.Abroad, hubAddrProbe{Name: p.name, OK: err == nil, Error: errTextOrEmpty(err)})
			if err != nil {
				r.Blockers = append(r.Blockers, "из-за рубежа: "+p.name+": "+err.Error())
			}
		}
	}

	relays := 0
	okRU := false
	for _, n := range nodes {
		if n.Role != "relay" {
			continue
		}
		relays++
		p := hubAddrProbe{Name: n.Name + " (" + n.IP + ")"}
		if rem, err := e.env.DialNode(ctx, n.ID); err != nil {
			p.Error = "SSH: " + err.Error()
		} else {
			out, err := rem.Run(ctx, hubRUProbeScript(nw))
			got := strings.TrimSpace(out)
			p.OK = err == nil && got == "200 "+strconv.Itoa(hubProbeSize)
			if !p.OK {
				p.Error = "получено: " + got
			}
		}
		okRU = okRU || p.OK
		r.Russia = append(r.Russia, p)
	}
	if relays > 0 && !okRU {
		r.Blockers = append(r.Blockers, "ни с одного входа в РФ новый адрес не отдал 256 КБ")
	}

	r.Bamboo = "после переключения" // hysteria answers from the route source only
	r.Phone = "not_done"
	switch {
	case e.phoneOK(c):
		r.Phone = "ok"
	case c.PhoneSkipped:
		r.Phone = "skipped"
	default:
		r.Blockers = append(r.Blockers, "нет проверки с телефона по мобильной сети (или пропустите её явно)")
	}

	r.Passed = len(r.Blockers) == 0
	if r.Passed {
		lg.printf("проверки пройдены")
		return r, e.advance(ctx, c, hubAddrChecked, nil)
	}
	if cur, err := e.env.Repo.Get(ctx, c.ID); err == nil && cur.State == hubAddrChecked {
		_ = e.env.Repo.SetState(ctx, c.ID, hubAddrChecked, hubAddrAdded, nil)
	}
	return r, fmt.Errorf("проверка не пройдена: %s", strings.Join(r.Blockers, "; "))
}

// serviceVerdict: ok, skipped (errHubCheckSkipped: the service is not set up) or the error.
func serviceVerdict(err error) string {
	if errors.Is(err, errHubCheckSkipped) {
		return "skipped"
	}
	return errText(err)
}

func errTextOrEmpty(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// hubRUProbeScript runs on an entry: 256 KiB from the Hub's new address with the site's name.
func hubRUProbeScript(nw netip.Addr) string {
	return fmt.Sprintf("curl -sS -m 30 --resolve %s:443:%s -o /dev/null -w '%%{http_code} %%{size_download}' https://%s/hub-probe.bin\n",
		hubSiteName, nw, hubSiteName)
}

func (e *hubAddrEngine) phoneRecord(c hubAddrChange) hubAddrPhoneRecord {
	var p hubAddrPhoneRecord
	if s, ok := c.Steps[hubStepPhone]; ok && len(s.Checks) > 0 {
		_ = json.Unmarshal(s.Checks, &p)
	}
	return p
}

func (e *hubAddrEngine) phoneOK(c hubAddrChange) bool {
	p := e.phoneRecord(c)
	if p.Target != "new" {
		return false
	}
	for _, r := range p.Results {
		if r.OK {
			return true
		}
	}
	return false
}

// StartPhoneCheck starts the phone page on the new address (which "new") or, for comparison, on
// the current one ("current").
func (e *hubAddrEngine) StartPhoneCheck(ctx context.Context, id int64, which string) (string, time.Time, error) {
	c, err := e.env.Repo.Get(ctx, id)
	if err != nil {
		return "", time.Time{}, err
	}
	if hubAddrFinal(c.State) {
		return "", time.Time{}, fmt.Errorf("смена уже в состоянии «%s»", c.State)
	}
	bind := c.NewIP
	if which == "current" {
		bind = c.OldIP
	} else {
		which = "new"
	}
	url, exp, err := e.env.PhoneStart(ctx, bind)
	if err != nil {
		return "", time.Time{}, err
	}
	b, _ := json.Marshal(hubAddrPhoneRecord{Target: which, URL: url, Expires: exp})
	now := e.env.Now().UTC()
	return url, exp, e.env.Repo.SaveStep(ctx, id, hubStepPhone, hubAddrStep{Status: hubStepRunning, StartedAt: &now, Checks: b})
}

// PhoneCheck reads what reached the phones and keeps it in the record.
func (e *hubAddrEngine) PhoneCheck(ctx context.Context, id int64) (hubAddrPhoneRecord, error) {
	c, err := e.env.Repo.Get(ctx, id)
	if err != nil {
		return hubAddrPhoneRecord{}, err
	}
	p := e.phoneRecord(c)
	active, results, err := e.env.PhoneRead(ctx)
	if err != nil {
		return p, err
	}
	p.Results = results
	s := c.Steps[hubStepPhone]
	s.Status = hubStepRunning
	if !active {
		s.Status = hubStepOK
	}
	s.Checks, _ = json.Marshal(p)
	return p, e.env.Repo.SaveStep(ctx, id, hubStepPhone, s)
}

// SkipPhone records the owner's explicit «без проверки с телефона».
func (e *hubAddrEngine) SkipPhone(ctx context.Context, id int64) error {
	c, err := e.env.Repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if hubAddrFinal(c.State) || c.State == hubAddrSwitching || c.State == hubAddrTransition {
		return fmt.Errorf("смена уже в состоянии «%s»", c.State)
	}
	return e.env.Repo.SetState(ctx, id, c.State, c.State, func(c *hubAddrChange) { c.PhoneSkipped = true })
}
