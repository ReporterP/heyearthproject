package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/hubnet"
	"github.com/heyearth/backend/internal/provision"
)

// Production side effects of the Hub address wizard.

// errHubCheckSkipped: a service check that does not apply (SMTP off, ЮKassa not configured).
var errHubCheckSkipped = errors.New("не настроено")

func defaultHubAddrEnv() hubAddrEnv {
	return hubAddrEnv{
		Repo:      sqlHubAddrRepo{db: db.DB},
		Hub:       provision.LocalRemote{},
		Nodes:     admin.NodeProvisioner,
		Jobs:      admin.NodeJobs,
		ListNodes: listHubAddrNodes,
		DialNode: func(ctx context.Context, id int) (provision.Remote, error) {
			n, err := nodeStore.LoadNode(ctx, id)
			if err != nil {
				return nil, err
			}
			return provision.DialNode(n)
		},
		Now:      time.Now,
		Sleep:    func(ctx context.Context, d time.Duration) error { return sleepCtxHubAddr(ctx, d) },
		Bound:    func(a netip.Addr) hubnet.DialFunc { return hubnet.BoundDialer(a, 3*time.Second) },
		Resolve:  hubnet.GoResolve,
		PickExit: func() (int, error) { return entryMon.pickExit() },
		RealityTLS: func(socks int, host string, port int) error {
			sni, err := subscriptionSetting("reality_sni_domain", biz.DefaultRealitySNI)
			if err != nil {
				return err
			}
			return entryTLSCheck(socks, host, port, sni)
		},
		SiteTLS:   hubSiteTLSCheck,
		CertCheck: hubLocalCertCheck,
		Bamboo: func(socks int, host string) error {
			_, _, err := probeBamboo(socks, host, []byte(biz.GetHysteria2ObfsPassword()))
			return err
		},
		Setting:     subscriptionSetting,
		SetSetting:  biz.SetSetting,
		ResetHubIPs: hubIPs.reset,
		SMTPAuth:    hubSMTPAuth,
		YooKassa:    hubYooKassaMe,
		PhoneStart: func(ctx context.Context, bind string) (string, time.Time, error) {
			return admin.StartMobileCheckOn(ctx, provision.LocalRemote{}, bind, bind)
		},
		PhoneRead: func(ctx context.Context) (bool, []admin.MobileCheckResult, error) {
			return admin.ReadMobileCheckOn(ctx, provision.LocalRemote{})
		},
		ExitTunnels: hubExitTunnels,
		Sync:        RequestXraySync,
		Alert:       func(key, text string) { RaiseAlert(key, AlertCritical, "Смена адреса хаба", text) },
		Go:          func(f func()) { go f() },
	}
}

func sleepCtxHubAddr(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// initHubAddr wires the engine once the DB and the provisioning runner are up: steps left running
// by the previous process become interrupted (the owner re-runs them).
func initHubAddr() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if n, err := markHubAddrInterrupted(ctx, db.DB); err != nil {
		log.Printf("[hub-address] interrupted steps not marked: %v", err)
	} else if n > 0 {
		log.Printf("[hub-address] %d шаг(ов) смены адреса прерваны перезапуском API — запустите их снова", n)
	}
	hubAddr = newHubAddrEngine(defaultHubAddrEnv())
}

// hubExitTunnels: tunnel_status of every online exit (the tunnel monitor writes it every 30 s).
func hubExitTunnels(ctx context.Context) ([]hubAddrProbe, error) {
	rows, err := db.DB.QueryContext(ctx, `SELECT COALESCE(name, ''), COALESCE(tunnel_status, '') FROM servers
		WHERE role = 'exit' AND lifecycle_state = 'online' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []hubAddrProbe
	for rows.Next() {
		var name, status string
		if err := rows.Scan(&name, &status); err != nil {
			return nil, err
		}
		p := hubAddrProbe{Name: "туннель к " + name, OK: status == "healthy"}
		if !p.OK {
			p.Error = "tunnel_status " + status
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func listHubAddrNodes(ctx context.Context) ([]hubAddrNode, error) {
	rows, err := db.DB.QueryContext(ctx, `SELECT id, COALESCE(name, ''), role, COALESCE(ip_address, ''),
		COALESCE(lifecycle_state, ''), COALESCE(ssh_port, 22), ssh_host_key_confirmed_at IS NOT NULL
		FROM servers WHERE role IN ('exit', 'relay') ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []hubAddrNode
	for rows.Next() {
		var n hubAddrNode
		var life string
		if err := rows.Scan(&n.ID, &n.Name, &n.Role, &n.IP, &life, &n.SSHPort, &n.HostKeyConfirmed); err != nil {
			return nil, err
		}
		n.Online = life == "online"
		out = append(out, n)
	}
	return out, rows.Err()
}

// hubSiteTLSCheck: TLS to host:443 through the exit's SOCKS with the site's name — the
// certificate must verify (it is the site nginx answering, not Reality).
func hubSiteTLSCheck(socks int, host, serverName string) error {
	d, err := exitSocksDialer(socks)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, "443"))
	if err != nil {
		return err
	}
	defer conn.Close()
	return tls.Client(conn, &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12}).HandshakeContext(ctx)
}

// hubLocalCertCheck: TLS to host:443 from the Hub itself with serverName — the certificate must
// verify for that name.
func hubLocalCertCheck(ctx context.Context, host, serverName string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 5 * time.Second}, Config: &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12}}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, "443"))
	if err != nil {
		return err
	}
	return conn.Close()
}

// hubSMTPAuth: the mail account accepts a login from dial's address (errHubCheckSkipped when
// SMTP is off).
func hubSMTPAuth(ctx context.Context, dial hubnet.DialFunc) error {
	get := func(k string) string { v, _ := biz.GetSettingContext(ctx, k); return strings.TrimSpace(v) }
	if !strings.EqualFold(get("smtp_enabled"), "true") || get("smtp_host") == "" {
		return errHubCheckSkipped
	}
	port, _ := strconv.Atoi(get("smtp_port"))
	if port == 0 {
		port = 587
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return hubnet.ProbeSMTPAuth(ctx, dial, get("smtp_host"), port, get("smtp_user"), get("smtp_password"))
}

// hubYooKassaMe: an authenticated read-only ЮKassa call from dial's address (GET /v3/me).
func hubYooKassaMe(ctx context.Context, dial hubnet.DialFunc) error {
	shop, _ := biz.GetSettingContext(ctx, "yookassa_shop_id")
	secret, _ := biz.GetSettingContext(ctx, "yookassa_secret_key")
	if strings.TrimSpace(shop) == "" || strings.TrimSpace(secret) == "" {
		return errHubCheckSkipped
	}
	cl := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{DialContext: dial, TLSHandshakeTimeout: 10 * time.Second}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.yookassa.ru/v3/me", nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(strings.TrimSpace(shop), strings.TrimSpace(secret))
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
