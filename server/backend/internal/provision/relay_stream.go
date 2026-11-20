package provision

// TCP 443 and 80 of a relay through its nginx stream, and the Hub's trust of the relay's PROXY
// header (block 3M / M7, config/relay_stream.go): the Hub sees every client's real address.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/shellquote"
)

func (l relayLayout) streamFile(o RelayOptions) (File, error) {
	c, err := config.RelayNginxConfVia(o.HubIP, str(o.tunnel().hub))
	if err != nil {
		return File{}, err
	}
	return File{l.nginx, []byte(c), 0o644}, nil
}

// relayStreamSh checks the written config before nginx takes it: a bad file never replaces the
// running one (reload keeps the old workers on a failed config; start would not even begin).
const relayStreamSh = `nginx -t -q -c "$NGINX"
systemctl enable --quiet nginx
if systemctl is-active --quiet nginx; then systemctl reload nginx; else systemctl start nginx; fi
echo "nginx на входе принимает TCP 443 и 80 и передаёт их на Hub через туннель ($TUN_PEER), запасной путь — $HUB"
`

const relayStreamCheckSh = `if ! systemctl is-active --quiet nginx; then echo "RELAY_FAIL nginx не работает"; fi
if [ "$(systemctl is-enabled nginx 2>/dev/null)" != enabled ]; then echo "RELAY_FAIL nginx не включён в автозапуск"; fi
`

func (l relayLayout) streamApply(o RelayOptions) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		f, err := l.streamFile(o)
		if err != nil {
			return err
		}
		changed, err := r.PutFile(ctx, f.Path, f.Data, f.Mode)
		if err != nil {
			return err
		}
		if changed {
			s.Printf("записан %s", f.Path)
		}
		return l.run(ctx, r, s, "stream", o, relayStreamSh)
	}
}

func (l relayLayout) streamVerify(o RelayOptions) stepFunc {
	return func(ctx context.Context, r Remote, _ *State) error {
		f, err := l.streamFile(o)
		if err != nil {
			return err
		}
		if err := filesMatch(ctx, r, []File{f}); err != nil {
			return err
		}
		return l.check(ctx, r, "stream-check", o, relayStreamCheckSh)
	}
}

// ── the Hub's side ──

// hubRelayReloadSh: nginx takes the new trust only after `nginx -t`; port RelayHubPPPort is
// opened in ufw (when ufw is on) — trust is per source address, not per firewall rule.
var hubRelayReloadSh = fmt.Sprintf(`set -euo pipefail
nginx -t -q
systemctl reload nginx
if command -v ufw >/dev/null 2>&1 && LC_ALL=C ufw status 2>/dev/null | grep -q '^Status: active'; then
  ufw allow %[1]d/tcp comment heyearth-relay-pp >/dev/null
fi
echo "Hub: nginx принимает входы на порту %[1]d"
`, config.RelayHubPPPort)

// relayHubTrust adds (on) or removes the Hub's trust of relayIP and of its tunnel address relayTun
// ("" = none): the relays' stream server (regenerated from the 443 router's SNI map) and the
// relay's set_real_ip_from file, then a checked reload. A refused config takes the relay's file
// away again. Swapped in tests (the relay tests run against a stub Hub).
var relayHubTrust = func(ctx context.Context, hub Remote, relayIP, relayTun string, on bool) error {
	if hub == nil {
		return errors.New("нет доступа к Hub")
	}
	trustPath := config.HubRelayTrustPath(relayIP)
	if !on {
		out, err := hub.Run(ctx, "rm -f "+shellquote.Quote(trustPath)+" && nginx -t -q && systemctl reload nginx")
		if err != nil {
			return fmt.Errorf("доверие входу %s на Hub не снято: %w: %s", relayIP, err, strings.TrimSpace(out))
		}
		return nil
	}
	router, err := hub.Run(ctx, "cat "+shellquote.Quote(config.HubStreamRouterPath))
	if err != nil {
		return fmt.Errorf("не прочитан %s на Hub: %w", config.HubStreamRouterPath, err)
	}
	names, err := config.RouterSiteNames(router)
	if err != nil {
		return err
	}
	trust, err := config.HubRelayTrustFileVia(relayIP, relayTun)
	if err != nil {
		return err
	}
	if _, err := hub.PutFile(ctx, config.HubRelayPPConfPath, []byte(config.HubRelayPPConf(names)), 0o644); err != nil {
		return err
	}
	if _, err := hub.PutFile(ctx, trustPath, []byte(trust), 0o644); err != nil {
		return err
	}
	if out, err := hub.Run(ctx, hubRelayReloadSh); err != nil {
		_, _ = hub.Run(ctx, "rm -f "+shellquote.Quote(trustPath))
		return fmt.Errorf("nginx на Hub не принял настройку входа: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

// relayHubTrustCheck: the relay's trust file is in place and the Hub listens on RelayHubPPPort.
var relayHubTrustCheck = func(ctx context.Context, hub Remote, relayIP, relayTun string) error {
	if hub == nil {
		return errors.New("нет доступа к Hub")
	}
	trust, err := config.HubRelayTrustFileVia(relayIP, relayTun)
	if err != nil {
		return err
	}
	if err := filesMatch(ctx, hub, []File{{config.HubRelayTrustPath(relayIP), []byte(trust), 0o644}}); err != nil {
		return err
	}
	out, err := hub.Run(ctx, fmt.Sprintf("ss -Hltn 'sport = :%d' | head -1", config.RelayHubPPPort))
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) == "" {
		return fmt.Errorf("Hub не слушает порт %d — входы не дойдут", config.RelayHubPPPort)
	}
	return nil
}
