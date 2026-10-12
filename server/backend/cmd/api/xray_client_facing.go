package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/heyearth/backend/internal/biz"
)

// ─────────────── Subscription notification (subscription_version) ───────────────
// BumpSubscriptionVersion makes every client show "subscription updated" and
// refetch. It used to run on every xray restart, most of which (exit failover,
// masking/direct rules, plan servers) change nothing a client has in its
// profile. It now runs only when the client-facing part of the config changed.
//
// What the subscription URIs pin (client_api.go buildHubConnectionOptions,
// subscription_handler.go) of the Hub's VLESS inbounds — the inbounds with
// clients: vless-reality (Берёза: sni, pbk, sid, type=tcp, flow), vless-xhttp
// (Дуб: port, sni, pbk, sid, type=xhttp, path, host, mode) and, while
// cdn_enabled, vless-xhttp-cdn (Облако: path, host, mode; it appears and
// disappears with the flag). So the digest covers, per such inbound: tag,
// protocol, port, decryption, the set of client flows, network, security,
// realitySettings.serverNames/shortIds/privateKey (pbk is derived from it) and
// xhttpSettings.path/host/mode. Server-side tuning (sockopt, sniffing, padding,
// dest/xver, policy), outbounds and routing are not client-facing. Hysteria2,
// fingerprints (fixed in the URIs) and main_server_host as the connect address
// do not go through the xray config at all.

// xrayClientFacingPath stores the digest clients were last notified of, next
// to the stale marker.
const xrayClientFacingPath = "/opt/heyearth/state/xray-client-facing.sha256"

// xrayClientFacing returns the digest of the client-facing part of configJSON.
func xrayClientFacing(configJSON []byte) (string, error) {
	var cfg struct {
		Inbounds []map[string]any `json:"inbounds"`
	}
	if err := json.Unmarshal(configJSON, &cfg); err != nil {
		return "", fmt.Errorf("parse config: %w", err)
	}
	proj := map[string]any{}
	for _, in := range cfg.Inbounds {
		settings, _ := in["settings"].(map[string]any)
		rawClients, ok := settings["clients"]
		if !ok {
			continue // not a per-user VLESS inbound (api-in, socks-awg)
		}
		tag, _ := in["tag"].(string)
		flowSet := map[string]bool{}
		list, _ := rawClients.([]any)
		for _, rc := range list {
			c, _ := rc.(map[string]any)
			f, _ := c["flow"].(string)
			flowSet[f] = true
		}
		flows := make([]string, 0, len(flowSet))
		for f := range flowSet {
			flows = append(flows, f)
		}
		sort.Strings(flows)
		ss, _ := in["streamSettings"].(map[string]any)
		p := map[string]any{
			"protocol":   in["protocol"],
			"port":       in["port"],
			"decryption": settings["decryption"],
			"flows":      flows,
			"network":    ss["network"],
			"security":   ss["security"],
		}
		if rs, ok := ss["realitySettings"].(map[string]any); ok {
			p["reality"] = map[string]any{
				"serverNames": rs["serverNames"],
				"shortIds":    rs["shortIds"],
				"privateKey":  rs["privateKey"],
			}
		}
		if xs, ok := ss["xhttpSettings"].(map[string]any); ok {
			p["xhttp"] = map[string]any{"path": xs["path"], "host": xs["host"], "mode": xs["mode"]}
		}
		proj[tag] = p
	}
	b, err := json.Marshal(proj) // map keys are sorted: canonical
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// subscriptionNotifier bumps subscription_version when the client-facing digest
// of the config in service differs from the one clients were last notified of.
type subscriptionNotifier struct {
	path string                          // the notified digest; "" = memory only (tests)
	bump func(ctx context.Context) error // biz.BumpSubscriptionVersionContext
	mem  string                          // the notified digest when path == ""
}

var xraySubscriptionNotifier = &subscriptionNotifier{path: xrayClientFacingPath, bump: biz.BumpSubscriptionVersionContext}

// afterApply runs after every successful apply (unchanged, live or restart)
// with the config now in service and the one it replaced (oldErr: it could not
// be read). Without a stored digest (first run of this code, lost state file)
// a restart is compared with the replaced config and a non-restart only
// records the digest. A failed bump keeps the old digest: the next sync
// retries. It returns whether clients were notified.
func (n *subscriptionNotifier) afterApply(newJSON, oldJSON []byte, oldErr error, restarted bool) bool {
	newH, err := xrayClientFacing(newJSON)
	if err != nil {
		log.Printf("[xray-sync] WARN: client-facing digest: %v — подписка не уведомлена", err)
		return false
	}
	stored, err := n.load()
	switch {
	case err == nil && stored == newH:
		return false
	case err == nil:
		// changed since clients were last notified
	case errors.Is(err, os.ErrNotExist):
		changed := restarted
		if restarted && oldErr == nil {
			if oldH, herr := xrayClientFacing(oldJSON); herr == nil {
				changed = oldH != newH
			}
		}
		if !changed {
			n.store(newH)
			return false
		}
	default:
		log.Printf("[xray-sync] WARN: %v — уведомляем подписки на всякий случай", err)
	}
	if _, err := withSyncTimeout(func(ctx context.Context) (struct{}, error) { return struct{}{}, n.bump(ctx) }); err != nil {
		log.Printf("[xray-sync] WARN: subscription_version не увеличен: %v (повтор при следующей синхронизации)", err)
		return false
	}
	n.store(newH)
	log.Println("[xray-sync] клиентские параметры подписки изменились — subscription_version увеличен")
	return true
}

func (n *subscriptionNotifier) load() (string, error) {
	if n.path == "" {
		if n.mem == "" {
			return "", os.ErrNotExist
		}
		return n.mem, nil
	}
	b, err := os.ReadFile(n.path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func (n *subscriptionNotifier) store(h string) {
	if n.path == "" {
		n.mem = h
		return
	}
	if err := os.WriteFile(n.path, []byte(h+"\n"), 0o600); err != nil {
		log.Printf("[xray-sync] WARN: %s не записан: %v", n.path, err)
	}
}
