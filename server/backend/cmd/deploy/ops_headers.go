package main

// Security headers of the site, the API and the admin panel (`deploy --setup-hardening`; the
// vhost generator buildNginxConfSSL includes the snippets in every server and in every location
// that sets its own add_header — nginx does not inherit add_header into such a location).
//
// Site (also the API, go.<domain> and the Telegram Mini App): HSTS, nosniff, Referrer-Policy,
// Permissions-Policy, framing enforced to the site itself and Telegram Web (the Mini App trusts a
// frame as a Telegram host — site/src/lib/telegram.js) and a full CSP in Report-Only mode first (reports to /api/v1/csp-report): the
// ЮKassa widget and Telegram Web (which frames the Mini App) must keep working; enforce after the
// reports are clean (runbook). Admin: the same plus an enforced CSP and no framing at all.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

const nginxHeadersDir = "/etc/nginx/heyearth-headers"

const headersCommon = `access_log /var/log/nginx/access.log he_masked;
add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;
add_header X-Content-Type-Options "nosniff" always;
add_header Permissions-Policy "camera=(), microphone=(), geolocation=(), usb=(), browsing-topics=()" always;
`

// siteCSP: own origin + the ЮKassa widget (script, its iframes, its API); Telegram Web may frame
// the Mini App; images from https (news, avatars) are harmless.
const siteCSP = "default-src 'self'; script-src 'self' https://yookassa.ru; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob: https:; font-src 'self' data:; " +
	"connect-src 'self' https://yookassa.ru https://*.yookassa.ru https://*.yoomoney.ru; " +
	"frame-src https://yookassa.ru https://*.yookassa.ru https://yoomoney.ru https://*.yoomoney.ru; " +
	"frame-ancestors 'self' https://web.telegram.org https://*.telegram.org; base-uri 'self'; object-src 'none'; " +
	"form-action 'self' https://yookassa.ru https://*.yoomoney.ru; report-uri /api/v1/csp-report"

const adminCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https:; " +
	"font-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; object-src 'none'; form-action 'self'"

var (
	siteHeadersConf = "# Installed by deploy --setup-hardening (cmd/deploy/ops_headers.go)\n" + headersCommon +
		`add_header Referrer-Policy "strict-origin-when-cross-origin" always;
add_header Content-Security-Policy-Report-Only "` + siteCSP + `" always;
add_header Content-Security-Policy "frame-ancestors 'self' https://web.telegram.org https://*.telegram.org" always;
`
	adminHeadersConf = "# Installed by deploy --setup-hardening (cmd/deploy/ops_headers.go)\n" + headersCommon +
		`add_header Referrer-Policy "no-referrer" always;
add_header X-Frame-Options "DENY" always;
add_header Content-Security-Policy "` + adminCSP + `" always;
`
)

// headerSnippetsScript installs the two snippets (the vhost refers to them: they must exist
// before it is written, also on the --nginx path).
func headerSnippetsScript() string {
	return opsPutFileFunc + putFileCalls([]opsFile{
		{nginxHeadersDir + "/site.conf", "644", siteHeadersConf},
		{nginxHeadersDir + "/admin.conf", "644", adminHeadersConf},
	})
}

// vhostWithoutHeaders is the vhost as it was generated before the header includes: what the
// live file may still be (then it is safe to replace with the generator's current output).
func vhostWithoutHeaders(v string) string {
	r := strings.NewReplacer(
		"    # Security headers (ops_headers.go); repeated in every location that sets its own add_header.\n    include /etc/nginx/heyearth-headers/site.conf;\n", "",
		"        include /etc/nginx/heyearth-headers/site.conf;\n", "        add_header X-Content-Type-Options \"nosniff\" always;\n",
		" include /etc/nginx/heyearth-headers/site.conf; }", " }",
		"    include /etc/nginx/heyearth-headers/admin.conf;\n", "",
		" include /etc/nginx/heyearth-headers/admin.conf; }", " }",
	)
	return r.Replace(v)
}

// vhostWithout429 is the vhost before nginx's own 429 page (spec 06 §6.4a) was added.
func vhostWithout429(v string) string {
	var out []string
	for _, l := range strings.Split(v, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "# nginx's own 429") || t == "error_page 429 /429.html;" || t == "error_page 429 = @he429;" ||
			strings.HasPrefix(t, "location = /429.html ") || strings.HasPrefix(t, "location @he429 ") {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// previousVhostShas: the generator's earlier outputs the live file may still be ("a|b" for a
// shell case pattern); replacing one of them with the current output loses nothing. The script
// also accepts the sha it stored after its last write (/etc/heyearth/heyearth-vhost.sha256).
func previousVhostShas(v string) string {
	no429 := vhostWithout429(v)
	return sha256hex(no429) + "|" + sha256hex(vhostWithoutHeaders(no429))
}

func sha256hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// renderHeadersApply: snippets, then the vhost — replaced only when the live file is exactly the
// generator's output (old or new): a hand-edited vhost is left alone and the step fails. nginx -t
// before the reload (restore on failure), then the headers must be on the site and the admin.
func renderHeadersApply(domain string) string {
	if !siteDomainRe.MatchString(domain) {
		domain = "localhost"
	}
	vhost := buildNginxConfSSL(domain, "/etc/letsencrypt/live/"+domain, true)
	return strings.ReplaceAll(`set -eu
V=/etc/nginx/conf.d/heyearth.conf
BK=/opt/heyearth/state/guard/headers
mkdir -p "$BK"; chmod 700 "$BK"; rm -f "$BK"/*
cp -p "$V" "$BK/heyearth.conf"
for f in site admin; do [ -f `+nginxHeadersDir+`/$f.conf ] && cp -p `+nginxHeadersDir+`/$f.conf "$BK/$f.conf" || true; done
`+headerSnippetsScript()+`live=$(sha256sum "$V" | cut -c1-64)
stored=$(cat /etc/heyearth/heyearth-vhost.sha256 2>/dev/null || true)
NEW='`+base64.StdEncoding.EncodeToString([]byte(vhost))+`'
case "$live" in
  `+sha256hex(vhost)+`) echo "[ops] headers: vhost already current" ;;
  `+previousVhostShas(vhost)+`) put_file "$V" 644 "$NEW" ;;
  *) if [ -n "$stored" ] && [ "$live" = "$stored" ]; then put_file "$V" 644 "$NEW"
     else echo "[ops] FAILED: $V differs from the generator (hand edits?) — not touched; run deploy --nginx or merge by hand"; exit 1; fi ;;
esac
restore() { cp -p "$BK/heyearth.conf" "$V"; nginx -t >/dev/null 2>&1 && systemctl reload nginx; rm -f /etc/heyearth/heyearth-vhost.sha256; }
if ! nginx -t; then restore; echo "[ops] FAILED: nginx -t rejected the headers — restored"; exit 1; fi
systemctl reload nginx
sleep 1
site=$(curl -sk -D - -o /dev/null --resolve __D__:8443:127.0.0.1 https://__D__:8443/ | tr -d '\r')
adm=$(curl -sk -D - -o /dev/null --resolve admin.__D__:8443:127.0.0.1 https://admin.__D__:8443/ | tr -d '\r')
api=$(curl -sk -o /dev/null -w '%{http_code}' --resolve __D__:8443:127.0.0.1 https://__D__:8443/api/v1/health)
printf '%s\n' "$site" | grep -qi '^strict-transport-security:' && printf '%s\n' "$adm" | grep -qi '^content-security-policy:' && [ "$api" = 200 ] ||
  { restore; echo "[ops] FAILED: headers missing or the API stopped answering — restored"; exit 1; }
mkdir -p /etc/heyearth; sha256sum "$V" | cut -c1-64 > /etc/heyearth/heyearth-vhost.sha256
echo "[ops] headers: site $(printf '%s\n' "$site" | head -1 | awk '{print $2}') with HSTS + CSP report-only, admin $(printf '%s\n' "$adm" | head -1 | awk '{print $2}') with CSP + DENY framing, api $api"
`, "__D__", domain)
}

const headersRollback = `BK=/opt/heyearth/state/guard/headers
[ -f "$BK/heyearth.conf" ] && cp -p "$BK/heyearth.conf" /etc/nginx/conf.d/heyearth.conf
nginx -t && systemctl reload nginx
`
