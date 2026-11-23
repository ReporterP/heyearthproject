package synth

import (
	"context"
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

const (
	uriTCP   = "vless://11111111-2222-4333-8444-555555555555@203.0.113.1:443?encryption=none&security=reality&sni=storage.yandexcloud.net&fp=firefox&pbk=PBK-test&sid=ab12&type=tcp&tcpKeepAliveInterval=15&tcpKeepAliveIdle=100&flow=xtls-rprx-vision#%F0%9F%8C%B3%20%D0%91%D0%B5%D1%80%D1%91%D0%B7%D0%B0"
	uriXHTTP = "vless://11111111-2222-4333-8444-555555555555@203.0.113.5:2443?encryption=none&security=reality&sni=storage.yandexcloud.net&fp=chrome&pbk=PBK-test&sid=ab12&type=xhttp&path=%2F&host=203.0.113.1&mode=auto&tcpKeepAliveInterval=15&tcpKeepAliveIdle=100#%D0%94%D1%83%D0%B1"
	uriHY2   = "hysteria2://hy2%2Bpass@203.0.113.1:50443/?obfs=salamander&obfs-password=obfs&sni=tls.example.test&insecure=0&mport=30000-65000&hop_interval=15&upmbps=20&downmbps=80#%D0%91%D0%B0%D0%BC%D0%B1%D1%83%D0%BA%202"
)

func TestParseURIList(t *testing.T) {
	text := "happ://routing/onadd/e30=\n" + uriTCP + "\n\n  " + uriXHTTP + "  \n" + uriHY2 + "\n"
	for name, body := range map[string]string{"text": text, "base64": base64.StdEncoding.EncodeToString([]byte(text))} {
		got, err := ParseURIList([]byte(body))
		if err != nil || len(got) != 3 || got[0] != uriTCP || got[2] != uriHY2 {
			t.Errorf("%s: %d lines, %v", name, len(got), err)
		}
	}
	if _, err := ParseURIList([]byte(uriTCP + "\nэто не строка подписки\n")); err == nil || !strings.Contains(err.Error(), "строка 2") {
		t.Errorf("garbage: %v, want an error naming line 2", err)
	}
	if got, err := ParseURIList([]byte("  \n")); err == nil || len(got) != 0 {
		t.Errorf("empty body: %v %v", got, err)
	}
}

func outbound(t *testing.T, cfg map[string]any, tag string) map[string]any {
	t.Helper()
	for _, o := range list(cfg["outbounds"]) {
		if m, _ := o.(map[string]any); m["tag"] == tag {
			return m
		}
	}
	t.Fatalf("no outbound %s in %v", tag, cfg["outbounds"])
	return nil
}

func TestFromURIVless(t *testing.T) {
	label, cfg, err := FromURI(uriTCP, 21900, nil)
	if err != nil || label != "🌳 Берёза" {
		t.Fatalf("tcp: %q %v", label, err)
	}
	if ins := list(cfg["inbounds"]); len(ins) != 1 || dig(ins[0], "protocol") != "socks" || dig(ins[0], "port") != 21900 {
		t.Errorf("inbounds %v", cfg["inbounds"])
	}
	o := outbound(t, cfg, "proxy")
	vnext := list(dig(o, "settings", "vnext"))[0]
	user := list(dig(vnext, "users"))[0]
	if dig(vnext, "address") != "203.0.113.1" || dig(vnext, "port") != 443 || dig(user, "id") != "11111111-2222-4333-8444-555555555555" || dig(user, "flow") != "xtls-rprx-vision" {
		t.Errorf("vnext %v", vnext)
	}
	rs := dig(o, "streamSettings", "realitySettings")
	if dig(rs, "serverName") != "storage.yandexcloud.net" || dig(rs, "publicKey") != "PBK-test" || dig(rs, "shortId") != "ab12" || dig(rs, "fingerprint") != "firefox" {
		t.Errorf("reality %v", rs)
	}
	if dig(o, "streamSettings", "network") != "tcp" {
		t.Errorf("network %v", dig(o, "streamSettings", "network"))
	}

	_, cfg, err = FromURI(uriXHTTP, 21901, &Via{Host: "127.0.0.1", Port: 1090})
	if err != nil {
		t.Fatal(err)
	}
	o = outbound(t, cfg, "proxy")
	if dig(o, "streamSettings", "network") != "xhttp" || dig(o, "streamSettings", "xhttpSettings", "host") != "203.0.113.1" || dig(o, "streamSettings", "xhttpSettings", "path") != "/" {
		t.Errorf("xhttp %v", o["streamSettings"])
	}
	if dig(o, "streamSettings", "sockopt", "dialerProxy") != viaTag {
		t.Errorf("via: %v", dig(o, "streamSettings", "sockopt"))
	}
}

func TestFromURIHysteria2(t *testing.T) {
	label, cfg, err := FromURI(uriHY2, 21902, nil)
	if err != nil || label != "Бамбук 2" {
		t.Fatalf("%q %v", label, err)
	}
	o := outbound(t, cfg, "proxy")
	if o["protocol"] != "hysteria" || dig(o, "settings", "address") != "203.0.113.1" || dig(o, "streamSettings", "hysteriaSettings", "auth") != "hy2+pass" {
		t.Errorf("hysteria %v", o)
	}
	if dig(o, "streamSettings", "tlsSettings", "serverName") != "tls.example.test" {
		t.Errorf("tls %v", dig(o, "streamSettings", "tlsSettings"))
	}
	if p := list(dig(o, "streamSettings", "finalmask", "udp")); len(p) != 1 || dig(p[0], "settings", "password") != "obfs" {
		t.Errorf("salamander %v", p)
	}
	if dig(o, "streamSettings", "finalmask", "quicParams", "udpHop", "ports") != "30000-65000" {
		t.Errorf("hop %v", dig(o, "streamSettings", "finalmask", "quicParams"))
	}
	// through a proxy the hop goes away and one port of the range is dialled (as ForWay does)
	_, cfg, err = FromURI(uriHY2, 21903, &Via{Host: "127.0.0.1", Port: 1090})
	if err != nil {
		t.Fatal(err)
	}
	o = outbound(t, cfg, "proxy")
	port, _ := dig(o, "settings", "port").(int)
	if dig(o, "streamSettings", "finalmask", "quicParams", "udpHop") != nil || port < 30000 || port > 65000 {
		t.Errorf("via: port %v hop %v", dig(o, "settings", "port"), dig(o, "streamSettings", "finalmask", "quicParams", "udpHop"))
	}
}

func TestFromURIRejects(t *testing.T) {
	for _, line := range []string{"trojan://x@203.0.113.1:443#t", "vless://11111111-2222-4333-8444-555555555555@:443?security=reality#x", "hysteria2://@203.0.113.1:50443#x", "not a uri"} {
		if _, _, err := FromURI(line, 21904, nil); err == nil {
			t.Errorf("%q accepted", line)
		}
	}
}

func TestRunnerTest(t *testing.T) {
	bin := stubCore(t)
	r := Runner{XrayBin: bin, WorkDir: t.TempDir()}
	_, cfg, err := FromURI(uriTCP, 21905, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SYNTH_STUB", "socks")
	if err := r.Test(context.Background(), cfg); err != nil {
		t.Errorf("an accepted config: %v", err)
	}
	t.Setenv("SYNTH_STUB", "reject")
	if err := r.Test(context.Background(), cfg); err == nil {
		t.Error("a config the core refuses passed")
	}
	if entries, _ := os.ReadDir(r.WorkDir); len(entries) != 0 {
		t.Errorf("the config file (it carries keys) is left: %v", entries)
	}
}
