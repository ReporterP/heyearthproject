package main

import (
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// A third backend behind the router (an MTProxy on 127.0.0.1:4443) would receive PROXY headers
// once `proxy_protocol on;` is set router-wide: switching on is refused, switching off is fine.
func TestTransformStreamRefusesForeignBackends(t *testing.T) {
	off := buildNginxStreamConf(sppDomain, false)
	withMTProxy := strings.Replace(off, "    default        xray_vless;\n", "    tg.hiearth.ru  mtproxy;\n    default        xray_vless;\n", 1) +
		"\nupstream mtproxy {\n    server 127.0.0.1:4443;\n}\n"
	if m, err := detectStreamMode(withMTProxy); err != nil || m != "off" {
		t.Fatalf("the mode of the router is still off: %q %v", m, err)
	}
	if _, err := transformStream(withMTProxy, "on"); err == nil || !strings.Contains(err.Error(), "127.0.0.1:4443") {
		t.Errorf("on with a third backend must be refused naming it, got %v", err)
	}
	direct := strings.Replace(off, "    default        xray_vless;\n", "    tg.hiearth.ru  127.0.0.1:4443;\n    default        xray_vless;\n", 1)
	if _, err := transformStream(direct, "on"); err == nil || !strings.Contains(err.Error(), "not one of our upstreams") {
		t.Errorf("a direct address as a map value must be refused, got %v", err)
	}
	remote := strings.Replace(off, "upstream nginx_https {\n    server 127.0.0.1:8443;\n}", "upstream nginx_https {\n    server 127.0.0.1:8443;\n    server 10.0.0.5:8443;\n}", 1)
	if _, err := transformStream(remote, "on"); err == nil {
		t.Error("a non-loopback server in a router upstream must be refused")
	}
	on := buildNginxStreamConf(sppDomain, true) + "\nupstream mtproxy {\n    server 127.0.0.1:4443;\n}\n"
	on = strings.Replace(on, "    default        xray_vless;\n", "    tg.hiearth.ru  mtproxy;\n    default        xray_vless;\n", 1)
	if _, err := transformStream(on, "off"); err != nil {
		t.Errorf("switching off must stay possible: %v", err)
	}
}

func TestCheckHTTPRealIP(t *testing.T) {
	if err := checkHTTPRealIP(sppSSLConf()); err != nil {
		t.Errorf("generator: %v", err)
	}
	other := strings.Replace(legacySSLConf(t), "    server_name admin.hiearth.ru;\n", "    server_name admin.hiearth.ru;\n    real_ip_header X-Forwarded-For;\n", 1)
	if err := checkHTTPRealIP(other); err == nil || !strings.Contains(err.Error(), "X-Forwarded-For") {
		t.Errorf("a vhost with another real_ip_header must be refused, got %v", err)
	}
	// a non-HTTPS (port 80) block may have anything
	plain := "server {\n    listen 80;\n    real_ip_header X-Real-IP;\n}\n"
	if err := checkHTTPRealIP(plain); err != nil {
		t.Errorf("port-80 block: %v", err)
	}
}

func TestCheckPPCoverage(t *testing.T) {
	on := buildNginxStreamConf(sppDomain, true)
	if err := checkPPCoverage(on, []string{sppSSLConf()}); err != nil {
		t.Fatalf("generator pair: %v", err)
	}
	extra := strings.Replace(on, "    default        xray_vless;\n", "    cdn.hiearth.ru nginx_https;\n    default        xray_vless;\n", 1)
	if err := checkPPCoverage(extra, []string{sppSSLConf()}); err == nil || !strings.Contains(err.Error(), "cdn.hiearth.ru") {
		t.Errorf("an SNI without a vhost on 8446 must be refused, got %v", err)
	}
	cdn := "server {\n    listen 8443 ssl http2;\n    listen 127.0.0.1:8446 ssl http2 proxy_protocol;\n    server_name cdn.hiearth.ru;\n}\n"
	if err := checkPPCoverage(extra, []string{sppSSLConf(), cdn}); err != nil {
		t.Errorf("covered by its own vhost: %v", err)
	}
	wild := "server {\n    listen 127.0.0.1:8446 ssl proxy_protocol;\n    server_name *.hiearth.ru;\n}\n"
	if err := checkPPCoverage(extra, []string{sppSSLConf(), wild}); err != nil {
		t.Errorf("covered by a wildcard vhost: %v", err)
	}
	noPP := "server {\n    listen 8443 ssl http2;\n    server_name cdn.hiearth.ru;\n}\n" // only the plain listener
	if err := checkPPCoverage(extra, []string{sppSSLConf(), noPP}); err == nil {
		t.Error("a vhost without the PROXY listener does not cover the SNI")
	}
	regex := strings.Replace(on, "    default        xray_vless;\n", "    ~^cdn\\.   nginx_https;\n    default        xray_vless;\n", 1)
	if err := checkPPCoverage(regex, []string{sppSSLConf()}); err == nil || !strings.Contains(err.Error(), "regex") {
		t.Errorf("a regex key cannot be verified: %v", err)
	}
}

// planStreamSwitch refuses before anything is rendered when a vhost has another real_ip_header
// or an SNI would land on the 8446 default server.
func TestPlanStreamSwitchReviewChecks(t *testing.T) {
	r, h := "/etc/nginx/stream.d/heyearth.conf", "/etc/nginx/conf.d/heyearth.conf"
	files := map[string]string{r: buildNginxStreamConf(sppDomain, false),
		h: strings.Replace(legacySSLConf(t), "    server_name admin.hiearth.ru;\n", "    server_name admin.hiearth.ru;\n    real_ip_header X-Forwarded-For;\n", 1)}
	if _, _, _, err := planStreamSwitch(fakeHub(files, sppXrayBoth), "on"); err == nil || !strings.Contains(err.Error(), "real_ip_header") {
		t.Errorf("real_ip_header conflict: %v", err)
	}
	files[h] = legacySSLConf(t)
	files[r] = strings.Replace(files[r], "    default        xray_vless;\n", "    cdn.hiearth.ru nginx_https;\n    default        xray_vless;\n", 1)
	if _, _, _, err := planStreamSwitch(fakeHub(files, sppXrayBoth), "on"); err == nil || !strings.Contains(err.Error(), "cdn.hiearth.ru") {
		t.Errorf("uncovered SNI: %v", err)
	}
	if _, _, _, err := planStreamSwitch(fakeHub(files, sppXrayBoth), "off"); err != nil {
		t.Errorf("off needs no coverage: %v", err)
	}
}

// After an SSH error at systemd-run the CLI reconnects and checks whether the switch runs before
// it declares a failure.
func TestLaunchStreamSwitch(t *testing.T) {
	ok := func(string) (string, error) { return "", nil }
	lost := func(string) (string, error) { return "", errors.New("connection lost") }
	runner := func(out string) remoteRun {
		return func(string) (string, error) { return out, nil }
	}
	if _, err := launchStreamSwitch(ok, nil, 0, func() {}); err != nil {
		t.Errorf("clean launch: %v", err)
	}
	for name, c := range map[string]struct {
		hub    string
		wantOK bool
	}{
		"unit active":                    {"active\n", true},
		"unit already done, log has run": {"inactive\n2026 [stream-proxy] start: --stream-proxy-protocol on\n", true},
		"nothing started":                {"inactive\n", false},
		"failed unit, no log":            {"failed\n", false},
	} {
		r, err := launchStreamSwitch(lost, func() (remoteRun, error) { return runner(c.hub), nil }, 3, func() {})
		if (err == nil) != c.wantOK {
			t.Errorf("%s: err %v", name, err)
		}
		if c.wantOK {
			if out, _ := r("x"); out != c.hub {
				t.Errorf("%s: must continue on the reconnected runner", name)
			}
		}
	}
	redials := 0
	if _, err := launchStreamSwitch(lost, func() (remoteRun, error) { redials++; return nil, errors.New("down") }, 0, func() {}); err == nil || redials != 3 {
		t.Errorf("unreachable Hub: err %v after %d redials", err, redials)
	}
	if streamSwitchPollTimeout < 6*time.Minute {
		t.Errorf("the poll timeout %v is shorter than the switch's worst case (~6 min)", streamSwitchPollTimeout)
	}
}

func TestClassifyExternalProbe(t *testing.T) {
	if err := classifyExternalProbe("203.0.113.9", 200, nil, nil); err != nil {
		t.Errorf("real IP: %v", err)
	}
	for name, err := range map[string]error{
		"request failed": classifyExternalProbe("", 0, nil, errors.New("timeout")),
		"HTTP 502":       classifyExternalProbe("203.0.113.9", 502, nil, nil),
		"log unreadable": classifyExternalProbe("", 200, errors.New("ssh"), nil),
		"not logged":     classifyExternalProbe("", 200, nil, nil),
		"127.0.0.1":      classifyExternalProbe("127.0.0.1", 200, nil, nil),
	} {
		if err == nil {
			t.Errorf("%s must be an error (non-zero exit)", name)
		}
	}
}

// deploy --status shows the live mode and the four entry points, and warns when the mode is on
// but the PROXY Reality inbound is missing (an API without the mirror).
func TestStreamStatusReport(t *testing.T) {
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	all := "LISTEN 0 511 127.0.0.1:8443 0.0.0.0:*\nLISTEN 0 511 127.0.0.1:8446 0.0.0.0:*\nLISTEN 0 4096 127.0.0.1:10443 0.0.0.0:*\nLISTEN 0 4096 127.0.0.1:10444 0.0.0.0:*\n"
	on := strings.Join(streamStatusReport(b64(buildNginxStreamConf(sppDomain, true))+"\n--- listeners\n"+all, nil), "\n")
	if !strings.Contains(on, "ВКЛ") || strings.Contains(on, "ВНИМАНИЕ") || !strings.Contains(on, "127.0.0.1:10444 — слушает") {
		t.Errorf("on, all up:\n%s", on)
	}
	noMirror := strings.Replace(all, "LISTEN 0 4096 127.0.0.1:10444 0.0.0.0:*\n", "", 1)
	bad := strings.Join(streamStatusReport(b64(buildNginxStreamConf(sppDomain, true))+"\n--- listeners\n"+noMirror, nil), "\n")
	if !strings.Contains(bad, "127.0.0.1:10444 — НЕ слушает") || !strings.Contains(bad, "Reality не работает") {
		t.Errorf("on without the mirror must warn:\n%s", bad)
	}
	off := strings.Join(streamStatusReport("ABSENT\n--- listeners\n"+noMirror, nil), "\n")
	if !strings.Contains(off, "выкл") || strings.Contains(off, "ВНИМАНИЕ") {
		t.Errorf("off without the mirror is fine:\n%s", off)
	}
	if r := streamStatusReport("", errors.New("ssh")); len(r) != 1 || !strings.Contains(r[0], "не прочитан") {
		t.Errorf("read error: %v", r)
	}
	half := strings.Replace(buildNginxStreamConf(sppDomain, true), "127.0.0.1:10444", "127.0.0.1:10443", 1)
	if r := strings.Join(streamStatusReport(b64(half)+"\n--- listeners\n"+all, nil), "\n"); !strings.Contains(r, "НЕ ОПРЕДЕЛЯЕТСЯ") {
		t.Errorf("half-switched router:\n%s", r)
	}
}

// The API-rollback warning (switch the stream off before an API without the mirror) is in the
// PROXY runbook, the restore runbook's rollback section and CLAUDE.md.
func TestStreamProxyRollbackDocs(t *testing.T) {
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	spp := read("../../../../docs/runbooks/stream-proxy-protocol.md")
	if !strings.Contains(spp, "Перед откатом API ниже b7d653b") || !strings.Contains(spp, "rolled-back-to-on") || !strings.Contains(spp, "go run ./cmd/deploy --status") {
		t.Error("the PROXY runbook lacks the API-rollback warning, the rolled-back wording or --status")
	}
	sec := runbookSection(t, read(restoreRunbook), "### Откат API ниже b7d653b, когда включён PROXY protocol на 443")
	if !strings.Contains(sec, "go run ./cmd/deploy --stream-proxy-protocol off") {
		t.Error("the restore runbook's rollback section must switch the stream off first")
	}
	if c := read("../../../../CLAUDE.md"); !strings.Contains(c, "below b7d653b") || !strings.Contains(c, "run `--stream-proxy-protocol off` first") {
		t.Error("CLAUDE.md lacks the API-rollback gotcha")
	}
}
