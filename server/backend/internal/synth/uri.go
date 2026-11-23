package synth

// The subscription as plain URIs (v2rayTun, INCY, Hiddify, Happ without the JSON profile): each
// line becomes a client config with one SOCKS inbound, so the same core that runs the JSON
// subscription can accept or refuse it (Runner.Test) and carry data through it (Runner.Probe).

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// ParseURIList reads a subscription body of URI lines, base64-encoded or as is. Happ's
// routing profile line (happ://) is not a proxy and is skipped; anything that is not a URI is
// an error naming its line.
func ParseURIList(body []byte) ([]string, error) {
	text := strings.TrimSpace(string(body))
	if text != "" && !strings.Contains(text, "://") {
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			if b, err := enc.DecodeString(strings.Join(strings.Fields(text), "")); err == nil {
				text = string(b)
				break
			}
		}
	}
	var out []string
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "happ://"):
			continue
		case !strings.Contains(line, "://"):
			return nil, fmt.Errorf("строка %d: не URI подключения", i+1)
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		return nil, errors.New("в подписке нет строк подключения")
	}
	return out, nil
}

// FromURI turns one vless:// or hysteria2:// line into a client config with one SOCKS inbound
// on 127.0.0.1:socksPort and the line's outbound (tag proxy) first; via dials it through a
// SOCKS proxy (an exit), dropping Hysteria's port hopping for one port of its range like ForWay.
func FromURI(line string, socksPort int, via *Via) (label string, cfg map[string]any, err error) {
	u, err := url.Parse(strings.TrimSpace(line))
	if err != nil {
		return "", nil, fmt.Errorf("URI: %w", err)
	}
	label, _ = url.PathUnescape(u.Fragment)
	host, portStr := u.Hostname(), u.Port()
	if host == "" {
		return "", nil, errors.New("URI: нет адреса")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return "", nil, errors.New("URI: нет порта")
	}
	q := u.Query()
	var out map[string]any
	switch strings.ToLower(u.Scheme) {
	case "vless":
		id := u.User.Username()
		if id == "" {
			return "", nil, errors.New("vless: нет id")
		}
		user := map[string]any{"id": id, "encryption": "none"}
		if f := q.Get("flow"); f != "" {
			user["flow"] = f
		}
		ss := map[string]any{"network": q.Get("type")}
		if ss["network"] == "" {
			ss["network"] = "tcp"
		}
		if q.Get("security") == "reality" {
			ss["security"] = "reality"
			ss["realitySettings"] = map[string]any{"serverName": q.Get("sni"), "publicKey": q.Get("pbk"), "shortId": q.Get("sid"),
				"fingerprint": q.Get("fp"), "spiderX": ""}
		}
		if ss["network"] == "xhttp" {
			ss["xhttpSettings"] = map[string]any{"path": q.Get("path"), "host": q.Get("host"), "mode": q.Get("mode")}
		}
		out = map[string]any{"tag": "proxy", "protocol": "vless", "streamSettings": ss,
			"settings": map[string]any{"vnext": []any{map[string]any{"address": host, "port": port, "users": []any{user}}}}}
	case "hysteria2", "hy2":
		pass := u.User.Username()
		if pass == "" {
			return "", nil, errors.New("hysteria2: нет пароля")
		}
		quic := map[string]any{"congestion": "bbr"}
		if up, down := q.Get("upmbps"), q.Get("downmbps"); up != "" && down != "" {
			quic = map[string]any{"congestion": "brutal", "brutalUp": up + " mbps", "brutalDown": down + " mbps"}
		}
		if r := q.Get("mport"); r != "" {
			interval, _ := strconv.Atoi(q.Get("hop_interval"))
			quic["udpHop"] = map[string]any{"ports": r, "interval": interval}
		}
		fm := map[string]any{"quicParams": quic}
		if q.Get("obfs") == "salamander" {
			fm["udp"] = []any{map[string]any{"type": "salamander", "settings": map[string]any{"password": q.Get("obfs-password")}}}
		}
		out = map[string]any{"tag": "proxy", "protocol": "hysteria", "settings": map[string]any{"address": host, "port": port, "version": 2},
			"streamSettings": map[string]any{"network": "hysteria", "security": "tls", "finalmask": fm,
				"hysteriaSettings": map[string]any{"version": 2, "auth": pass},
				"tlsSettings":      map[string]any{"serverName": q.Get("sni"), "alpn": []any{"h3"}}}}
	default:
		return "", nil, fmt.Errorf("URI: схема %q не проверяется", u.Scheme)
	}
	outbounds := []any{out}
	if via != nil {
		throughVia(out)
		outbounds = append(outbounds, viaOutbound(via))
	}
	outbounds = append(outbounds, map[string]any{"tag": "direct", "protocol": "freedom"})
	cfg = map[string]any{
		"log":       map[string]any{"loglevel": "warning"},
		"inbounds":  []any{socksInbound("socks-in", socksPort, nil)},
		"outbounds": outbounds,
	}
	if label == "" {
		label = net.JoinHostPort(host, portStr)
	}
	return label, cfg, nil
}

// Test asks the core whether it accepts cfg (`xray run -test -c <file>`): nil — accepted. The
// file carries keys and is removed before Test returns.
func (r Runner) Test(ctx context.Context, cfg map[string]any) error {
	if err := os.MkdirAll(r.WorkDir, 0o700); err != nil {
		return fmt.Errorf("work dir: %w", err)
	}
	dir, err := os.MkdirTemp(r.WorkDir, "test-")
	if err != nil {
		return fmt.Errorf("work dir: %w", err)
	}
	defer os.RemoveAll(dir)
	body, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	conf := filepath.Join(dir, "c.json")
	if err := os.WriteFile(conf, body, 0o600); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	cmd := exec.CommandContext(ctx, r.XrayBin, "run", "-test", "-c", conf)
	asset := r.AssetDir
	if asset == "" {
		asset = filepath.Dir(r.XrayBin)
	}
	cmd.Env = append(os.Environ(), "XRAY_LOCATION_ASSET="+asset)
	out, err := cmd.CombinedOutput()
	if err != nil {
		tail := strings.TrimSpace(string(out))
		if len(tail) > 300 {
			tail = tail[len(tail)-300:]
		}
		return fmt.Errorf("ядро не принимает конфиг: %v %s", err, tail)
	}
	return nil
}
