package config

import (
	"os/exec"
	"strings"
	"testing"
)

const tgTestSecret = "0123456789abcdef0123456789abcdef"

func TestTGWebRenderedGoldens(t *testing.T) {
	cfg, err := TGWebConfigJSON("tg.example.ru")
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "tgweb_config_json.golden", cfg)
	prof, err := TGWebProfilesJSON(tgTestSecret)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "tgweb_profiles_json.golden", prof)
	env, err := TGExitMTProxyEnv(tgTestSecret, "")
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "tgweb_mtproxy_env.golden", env)
	natEnv, err := TGExitMTProxyEnv(tgTestSecret, "--nat-info 10.0.0.5:203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "tgweb_mtproxy_env_nat.golden", natEnv)
	golden(t, "tgweb_nft.golden", []byte(TGExitMTProxyNft))
	golden(t, "tgweb_unit_relay.golden", []byte(TGWebRelayUnitText))
	golden(t, "tgweb_unit_mtproxy.golden", []byte(TGExitMTProxyUnitText))
	golden(t, "tgweb_unit_firewall.golden", []byte(TGExitFirewallUnitText))
	golden(t, "tgweb_unit_fwd.golden", []byte(TGFwdUnitText))
	golden(t, "tgweb_fwd_backends.golden", TGFwdBackendsJSON(map[int]string{3: "198.51.100.3", 2: "198.51.100.2", 9: "not-an-ip"}))
	golden(t, "tgweb_unit_refresh.golden", []byte(TGExitRefreshServiceText+"---\n"+TGExitRefreshTimerText+"---\n"+TGExitRefreshScriptText))
	http80, err := TGWebNginxConf("tg.example.ru", "")
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "tgweb_nginx_http.golden", []byte(http80))
	full, err := TGWebNginxConf("tg.example.ru", "/etc/letsencrypt/live/tg.example.ru")
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "tgweb_nginx_tls.golden", []byte(full))
}

// The nginx server must hold what the plan requires of it (a golden alone would freeze a mistake).
func TestTGWebNginxConfRequirements(t *testing.T) {
	c, _ := TGWebNginxConf("tg.example.ru", "/etc/letsencrypt/live/tg.example.ru")
	for _, want := range []string{
		"listen 8443 ssl http2;", "listen 127.0.0.1:8446 ssl http2 proxy_protocol;", "real_ip_header proxy_protocol;",
		"set_real_ip_from 127.0.0.1;", "access_log off;", "proxy_buffering off;", "proxy_request_buffering off;",
		"client_max_body_size 0;", "proxy_read_timeout 75s;", "proxy_set_header X-Forwarded-For $remote_addr;",
		"proxy_set_header Host $host;", "ssl_ecdh_curve X25519:prime256v1;", "proxy_set_header Upgrade $http_upgrade;",
		"proxy_http_version 1.1;", "server 127.0.0.1:8080;", "ssl_certificate /etc/letsencrypt/live/tg.example.ru/fullchain.pem;",
		"return 301 https://$host$request_uri;",
	} {
		if !strings.Contains(c, want) {
			t.Errorf("nginx conf lacks %q", want)
		}
	}
	for _, bad := range []string{"$proxy_add_x_forwarded_for", "X-Frame-Options", "Content-Security-Policy", "Cross-Origin", "hiearth"} {
		if strings.Contains(c, bad) {
			t.Errorf("nginx conf must not contain %q", bad)
		}
	}
	if strings.Count(c, "access_log off;") != 2 {
		t.Errorf("both server blocks must log nothing")
	}
	if _, err := TGWebNginxConf("tg.example.ru", "/x; evil"); err == nil {
		t.Error("a cert dir with ';' must be refused")
	}
}

func TestTGWebValidation(t *testing.T) {
	for _, h := range []string{"tg.hiearth.ru", "a.b", "xn--80ak6aa92e.xn--p1ai"} {
		if err := ValidTGWebHost(h); err != nil {
			t.Errorf("%q: %v", h, err)
		}
	}
	for _, h := range []string{"", "localhost", "TG.hiearth.ru", "tg.hiearth.ru/x", "tg hiearth.ru", "тг.рф", "tg.hiearth.ru;", "-a.ru"} {
		if ValidTGWebHost(h) == nil {
			t.Errorf("%q accepted", h)
		}
	}
	for _, s := range []string{"", "ABCDEF0123456789abcdef0123456789", "dd" + tgTestSecret, tgTestSecret[:30]} {
		if _, err := TGWebProfilesJSON(s); err == nil {
			t.Errorf("secret %q accepted", s)
		}
	}
	if _, err := TGExitMTProxyEnv(tgTestSecret, "--nat-info 1.2.3.4:5.6.7.8\nX=1"); err == nil {
		t.Error("a NAT value with a newline must be refused")
	}
	if _, err := TGWebTokenKey(strings.Repeat("ab", 31)); err == nil {
		t.Error("31-byte token key accepted")
	}
	if b, err := TGWebTokenKey(strings.Repeat("ab", 32)); err != nil || len(b) != 32 {
		t.Errorf("token key: %v %d", err, len(b))
	}
}

// The MTProxy port stays out of every loopback range the Hub's egress guard opens to xray.
func TestTGMTProxyPortOutsideGuardRanges(t *testing.T) {
	for _, r := range [][2]int{{1088, 1599}, {2088, 2599}, {12000, 12511}} {
		for _, p := range []int{TGMTProxyPort, TGExitMTProxyStatsPort, 8080, 8081} {
			if p >= r[0] && p <= r[1] {
				t.Errorf("port %d inside %d..%d", p, r[0], r[1])
			}
		}
	}
	for _, u := range []string{TGExitMTProxyUnitText, TGExitRefreshScriptText, TGExitMTProxyBuildScript, TGExitFirewallUnitText} {
		if rest := strings.ReplaceAll(u, "heyearth-tg-mtproxy", ""); strings.Contains(rest, "mtproxy.service") || strings.Contains(rest, "/opt/mtproxy") {
			t.Error("the exit's mtg (mtproxy.service, /opt/mtproxy) must never be named by the WEB proxy's MTProxy")
		}
	}
	if !strings.Contains(TGExitMTProxyUnitText, "-H 3398") || !strings.Contains(TGExitMTProxyNft, "{ 3398, 3399 }") || !strings.Contains(TGFwdUnitText, "-listen 127.0.0.1:3398") {
		t.Error("unit and nft table must use the same MTProxy ports")
	}
}

const tgTestRouter = `# SNI-based routing
map $ssl_preread_server_name $backend {
    hiearth.ru          nginx_https;
    go.hiearth.ru       nginx_https;
    default        xray_vless;
}

upstream nginx_https {
    server 127.0.0.1:8446;
}

upstream xray_vless {
    server 127.0.0.1:10444;
}
`

func TestRouterManagedLines(t *testing.T) {
	line, err := TGWebRouterLine("tg.hiearth.ru")
	if err != nil {
		t.Fatal(err)
	}
	with, err := RouterWithManagedLines(tgTestRouter, []string{line})
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "tgweb_router_with.golden", []byte(with))
	names, err := RouterSiteNames(with)
	if err != nil || strings.Join(names, ",") != "go.hiearth.ru,hiearth.ru,tg.hiearth.ru" {
		t.Fatalf("site names %v %v", names, err)
	}
	if got := RouterManagedLines(with); len(got) != 1 || got[0] != line {
		t.Fatalf("managed lines %q", got)
	}
	again, err := RouterWithManagedLines(with, []string{line})
	if err != nil || again != with {
		t.Fatalf("not idempotent: %v\n%s", err, again)
	}
	moved, _ := TGWebRouterLine("tg.other.ru")
	m2, err := RouterWithManagedLines(with, []string{moved})
	if err != nil || strings.Contains(m2, "tg.hiearth.ru") || !strings.Contains(m2, "tg.other.ru") {
		t.Fatalf("host change: %v\n%s", err, m2)
	}
	off, err := RouterWithManagedLines(with, nil)
	if err != nil || off != tgTestRouter {
		t.Fatalf("removal must restore the router:\n%s", off)
	}
	clash, _ := TGWebRouterLine("go.hiearth.ru")
	if _, err := RouterWithManagedLines(tgTestRouter, []string{clash}); err == nil {
		t.Error("a host the router already routes must be refused")
	}
	if _, err := RouterWithManagedLines("stream {}", []string{line}); err == nil {
		t.Error("a router without the map must be refused")
	}
	if _, err := RouterWithManagedLines(tgTestRouter, []string{"    evil nginx_https;"}); err == nil {
		t.Error("an unmarked line must be refused")
	}
}

func TestTGWebDNSCheck(t *testing.T) {
	exp := []string{"198.51.100.1", "198.51.100.2", "203.0.113.9"}
	cases := []struct {
		a, aaaa []string
		ok      bool
		has     string
	}{
		{[]string{"198.51.100.1", "198.51.100.2"}, nil, true, ""},
		{[]string{"203.0.113.9"}, nil, true, ""},
		{nil, nil, false, "нет A-записей"},
		{[]string{"198.51.100.1", "192.0.2.50"}, nil, false, "192.0.2.50"},
		{[]string{"198.51.100.1"}, []string{"2001:db8::1"}, false, "AAAA"},
	}
	for _, c := range cases {
		p := TGWebDNSCheck("tg.example.ru", c.a, c.aaaa, exp)
		if (p == "") != c.ok || (c.has != "" && !strings.Contains(p, c.has)) {
			t.Errorf("%v %v: %q", c.a, c.aaaa, p)
		}
	}
}

func TestTGWebSiteFiles(t *testing.T) {
	names, data := TGWebSiteFiles()
	if strings.Join(names, ",") != "404.html,about.html,favicon.svg,index.html,styles.css" {
		t.Fatalf("site files %v", names)
	}
	for n, b := range data {
		s := strings.ToLower(string(b))
		for _, bad := range []string{"hiearth", "heyearth", "telegram", "proxy", "прокси", "vpn", "<script", "@import", "https://", "//fonts"} {
			if strings.Contains(s, bad) {
				t.Errorf("%s contains %q", n, bad)
			}
		}
	}
}

// The shell the Hub runs parses (bash -n), when bash is around.
func TestTGWebScriptsParse(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	for name, s := range map[string]string{"build": TGExitMTProxyBuildScript, "nat": TGMTProxyNATScript, "refresh": TGExitRefreshScriptText} {
		c := exec.Command(bash, "-n")
		c.Stdin = strings.NewReader(s)
		if out, err := c.CombinedOutput(); err != nil {
			t.Errorf("%s: %v\n%s", name, err, out)
		}
	}
	if !strings.Contains(TGExitMTProxyBuildScript, "touch /opt/heyearth/state/restart-pending/heyearth-tg-mtproxy") || !strings.Contains(TGExitMTProxyBuildScript, "--no-upgrade") {
		t.Error("build script must mark the restart and never upgrade")
	}
}

// An MTProxy port beside the classic one is allowed on the exit's own address in all three
// places (hysteria ACL, ShadowTLS route, kernel guard of the stls user) and nowhere else.
func TestExitOwnPortsList(t *testing.T) {
	const ip = "203.0.113.7"
	hy := ExitHysteriaServerYAML("pw", ip, 8443, TGMTProxyPort)
	if !strings.Contains(hy, "direct(203.0.113.7, tcp/8443)") || !strings.Contains(hy, "direct(203.0.113.7, tcp/3398)") {
		t.Fatalf("hysteria ACL:\n%s", hy)
	}
	st, err := ExitSTLSServerJSON("0f0e0d0c-0b0a-4908-8706-050403020100", ip, 8443, TGMTProxyPort)
	if err != nil || !strings.Contains(string(st), `"port": [`) || !strings.Contains(string(st), "3398") {
		t.Fatalf("stls route: %v\n%s", err, st)
	}
	g, err := ExitGuardConf(ip, 8443, TGMTProxyPort)
	if err != nil || !strings.Contains(g, "ip daddr 203.0.113.7 tcp dport 3398 return") || !strings.Contains(g, "tcp dport 8443 return") {
		t.Fatalf("guard: %v\n%s", err, g)
	}
	if one := ExitHysteriaServerYAML("pw", ip, 8443); strings.Contains(one, "3398") {
		t.Fatal("one port in, one port out")
	}
}
