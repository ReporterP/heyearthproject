package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

// ── The subscription domain (app_settings sub_domain, e.g. go.hiearth.ru) ──
//
// Its A records point at the reserve entries in RU, not at the Hub: an entry
// forwards TCP 443/80 to the Hub (relay-pp.conf), and the Hub serves the name
// like the site (deploy --nginx adds it to nginx and to the certificate). The
// subscription link built on it refreshes on a mobile network that cuts the
// Hub's subnet; the cabinet opens through it too.

// Side effects of the status checks (tests swap them).
var (
	subDomainLookup = func(ctx context.Context, host string) ([]string, error) {
		return net.DefaultResolver.LookupHost(ctx, host)
	}
	subDomainTLS = checkSubDomainTLS
)

// subDomainNginx is the Hub's nginx HTTPS listener without PROXY protocol.
const subDomainNginx = "127.0.0.1:8443"

// checkSubDomainTLS asks the Hub's nginx for domain: the certificate must
// verify for that name (chain and hostname). Returns its end of validity.
func checkSubDomainTLS(ctx context.Context, domain string) (time.Time, error) {
	d := tls.Dialer{NetDialer: &net.Dialer{Timeout: 5 * time.Second}, Config: &tls.Config{ServerName: domain}}
	conn, err := d.DialContext(ctx, "tcp", subDomainNginx)
	if err != nil {
		return time.Time{}, err
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return time.Time{}, errors.New("нет сертификата")
	}
	return certs[0].NotAfter, nil
}

// subscriptionBaseURL is the root of the subscription links the cabinet hands
// out: https://<sub_domain> when set, else the site (publicBaseURL). A failed
// read of the setting falls back to the site: the link works there too.
func subscriptionBaseURL() string {
	d, err := subscriptionSetting("sub_domain", "")
	if err != nil {
		log.Printf("[subscribe] sub_domain: %v", err)
		return publicBaseURL()
	}
	if d = strings.TrimSpace(d); d != "" {
		return "https://" + d
	}
	return publicBaseURL()
}

// requestHost is r.Host without the port, lower case.
func requestHost(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(strings.TrimSpace(host))
}

// subDomainView is the «Подписка для мобильных» card of page «Входы».
type subDomainView struct {
	Domain       string     `json:"domain"`
	URL          string     `json:"url"` // root of the links the cabinet hands out now
	AutoMode     string     `json:"auto_mode"`
	AutoTestUser string     `json:"auto_test_user"`
	DNS          []string   `json:"dns"`
	DNSError     string     `json:"dns_error,omitempty"`
	DNSForeign   []string   `json:"dns_foreign"` // records that are no reserve entry's address
	DNSMissing   []string   `json:"dns_missing"` // online reserve entries without a record
	CertOK       bool       `json:"cert_ok"`
	CertUntil    *time.Time `json:"cert_until,omitempty"`
	CertError    string     `json:"cert_error,omitempty"`
}

func normalizeAutoMode(m string) string {
	switch m = strings.TrimSpace(m); m {
	case autoModeTest, autoModeOn:
		return m
	}
	return autoModeOff
}

// subDomainStatus checks domain (DNS against the reserve entries, the Hub's
// certificate for it). No domain: no checks.
func subDomainStatus(ctx context.Context, domain, autoMode, autoTestUser string, entries []biz.Entry) subDomainView {
	v := subDomainView{Domain: domain, URL: publicBaseURL(), AutoMode: normalizeAutoMode(autoMode),
		AutoTestUser: autoTestUser, DNS: []string{}, DNSForeign: []string{}, DNSMissing: []string{}}
	if domain == "" {
		return v
	}
	v.URL = "https://" + domain
	cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	ips, err := subDomainLookup(cctx, domain)
	if err != nil {
		v.DNSError = err.Error()
	}
	slices.Sort(ips)
	v.DNS = append(v.DNS, ips...)
	var relays []string
	for _, e := range entries {
		if e.Kind == biz.EntryKindRelay && e.Lifecycle == "online" {
			relays = append(relays, e.IP)
		}
	}
	for _, ip := range v.DNS {
		if !slices.Contains(relays, ip) {
			v.DNSForeign = append(v.DNSForeign, ip)
		}
	}
	for _, ip := range relays {
		if ip != "" && err == nil && !slices.Contains(v.DNS, ip) {
			v.DNSMissing = append(v.DNSMissing, ip)
		}
	}
	tctx, tcancel := context.WithTimeout(ctx, 6*time.Second)
	defer tcancel()
	until, err := subDomainTLS(tctx, domain)
	if err != nil {
		v.CertError = err.Error()
	} else {
		v.CertOK, v.CertUntil = true, &until
	}
	return v
}
