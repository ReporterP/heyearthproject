package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/config"
)

// go.<domain> is served like the site: the 443 router sends it to nginx (both PROXY modes), the
// entries' listener learns it from the router, and the site's server block answers to it.
func TestSiteAliasServedLikeTheSite(t *testing.T) {
	for _, pp := range []bool{false, true} {
		router := buildNginxStreamConf("hiearth.ru", pp)
		names, err := config.RouterSiteNames(router)
		if err != nil || !slices.Contains(names, "go.hiearth.ru") || !slices.Contains(names, "hiearth.ru") {
			t.Fatalf("pp=%v: site names %v %v", pp, names, err)
		}
		if pp := config.HubRelayPPConf(names); !strings.Contains(pp, "go.hiearth.ru") {
			t.Fatalf("relay-pp.conf without the alias:\n%s", pp)
		}
	}
	ssl := buildNginxConfSSL("hiearth.ru", "/etc/letsencrypt/live/hiearth.ru", true)
	if !strings.Contains(ssl, "server_name hiearth.ru www.hiearth.ru go.hiearth.ru;") {
		t.Errorf("the site block does not answer to go.hiearth.ru:\n%s", ssl)
	}
	if !strings.Contains(buildNginxConfHTTP("hiearth.ru"), "go.hiearth.ru") {
		t.Error("the plain-HTTP site does not answer to go.hiearth.ru")
	}
}

// The certificate step: a covered name and an unresolved one change nothing; otherwise certbot
// expands the certificate with every current name plus the alias, and nginx reloads.
func TestSiteAliasCertScript(t *testing.T) {
	stubs := func(san string, resolves bool) string {
		g := "exit 2"
		if resolves {
			g = "echo 203.0.113.10 STREAM go.hiearth.ru"
		}
		return `set -e
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
mkdir -p "$T/bin" "$T/le/live/hiearth.ru"; : > "$T/le/live/hiearth.ru/fullchain.pem"
cat > "$T/bin/openssl" <<'EOF'
#!/bin/sh
printf 'X509v3 Subject Alternative Name: \n    ` + san + `\n'
EOF
cat > "$T/bin/getent" <<'EOF'
#!/bin/sh
` + g + `
EOF
cat > "$T/bin/certbot" <<'EOF'
#!/bin/sh
echo "certbot $*" >> "$CALLS"
EOF
cat > "$T/bin/systemctl" <<'EOF'
#!/bin/sh
echo "systemctl $*" >> "$CALLS"
EOF
chmod +x "$T/bin/"*
export PATH="$T/bin:$PATH" CALLS="$T/calls"; : > "$CALLS"
`
	}
	script := strings.ReplaceAll(siteAliasCertScript("hiearth.ru"), "/etc/letsencrypt", "$T/le")
	for _, c := range []struct {
		name, san string
		resolves  bool
		want      string
		certbot   bool
	}{
		{"covered", "DNS:hiearth.ru, DNS:go.hiearth.ru", true, "OK сертификат уже покрывает", false},
		{"unread", "", true, "не прочитал имена", false},
		{"other lineage", "DNS:admin.hiearth.ru", true, "не прочитал имена", false},
		{"unresolved", "DNS:hiearth.ru, DNS:www.hiearth.ru", false, "не резолвится", false},
		{"expand", "DNS:hiearth.ru, DNS:admin.hiearth.ru, DNS:www.hiearth.ru", true, "OK сертификат теперь покрывает", true},
	} {
		out, exit := runBash(t, stubs(c.san, c.resolves)+"( "+script+" )\necho CALLS:; cat \"$CALLS\"\n")
		if exit != 0 || !strings.Contains(out, c.want) {
			t.Fatalf("%s: exit %d\n%s", c.name, exit, out)
		}
		ran := strings.Contains(out, "certbot certonly")
		if ran != c.certbot {
			t.Fatalf("%s: certbot ran=%v\n%s", c.name, ran, out)
		}
		if c.certbot && !strings.Contains(out, "--cert-name hiearth.ru --expand --non-interactive -d hiearth.ru -d admin.hiearth.ru -d www.hiearth.ru -d go.hiearth.ru") {
			t.Fatalf("%s: certbot args\n%s", c.name, out)
		}
	}
}
