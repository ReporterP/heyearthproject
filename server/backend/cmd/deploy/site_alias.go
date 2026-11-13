package main

import (
	"fmt"
	"strings"

	"github.com/heyearth/backend/internal/config"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// go.<domain> is the site's name through the reserve entries in RU: its A records point at the
// entries, not at the Hub, so the subscription link and the cabinet open on mobile networks that
// cut the Hub's subnet. The Hub serves it like the site (nginx server_name, the 443 router sends it
// to nginx), the entries' listener learns it from that router (relay-pp.conf), and the certificate
// covers it once the name resolves: Let's Encrypt checks http-01 through an entry, whose port 80
// goes to the Hub's port 80.
const siteAliasPrefix = "go."

// refreshRelayPP rewrites the Hub's listener for the reserve entries (relay-pp.conf) from the 443
// router just written, when the Hub has entries: its map of the site's names must follow the
// router's, or go.<domain> through an entry would land in xray.
func refreshRelayPP(c *sftp.Client, client *ssh.Client, router string) error {
	out, _ := runSSHQuiet(client, "test -f "+config.HubRelayPPConfPath+" && echo yes || echo no", "")
	if strings.TrimSpace(out) != "yes" {
		return nil
	}
	names, err := config.RouterSiteNames(router)
	if err != nil {
		return err
	}
	if err := uploadFileContent(c, []byte(config.HubRelayPPConf(names)), config.HubRelayPPConfPath); err != nil {
		return err
	}
	fmt.Printf("nginx: %s — имена сайта для входов: %s\n", config.HubRelayPPConfPath, strings.Join(names, ", "))
	return nil
}

// siteAliasCertScript adds go.<domain> to the site's certificate (certbot's nginx authenticator,
// the one the certificate was issued with) when the name resolves and the certificate lacks it.
// Idempotent: a covered name or an unresolved one changes nothing.
func siteAliasCertScript(domain string) string {
	alias := siteAliasPrefix + domain
	return fmt.Sprintf(`set -e
cert=/etc/letsencrypt/live/%[1]s/fullchain.pem
if [ ! -f "$cert" ]; then echo "SKIP нет сертификата $cert"; exit 0; fi
names=$(openssl x509 -in "$cert" -noout -ext subjectAltName 2>/dev/null | tr ',' '\n' | sed -n 's/.*DNS://p' | tr -d ' ')
if printf '%%s\n' $names | grep -qxF %[2]s; then echo "OK сертификат уже покрывает %[2]s"; exit 0; fi
if ! getent ahostsv4 %[2]s >/dev/null 2>&1; then echo "SKIP %[2]s не резолвится — сначала A-записи у регистратора"; exit 0; fi
args=""
for n in $names %[2]s; do args="$args -d $n"; done
certbot certonly --nginx --cert-name %[1]s --expand --non-interactive --quiet $args --deploy-hook 'systemctl reload nginx'
systemctl reload nginx
echo "OK сертификат теперь покрывает:" $names %[2]s
`, domain, alias)
}

// ensureSiteAliasCert runs siteAliasCertScript; a failure is reported, not fatal (the site keeps
// working on its other names; the admin shows whether go.<domain> has a valid certificate).
func ensureSiteAliasCert(client *ssh.Client, domain string) {
	out, err := runSSH(client, siteAliasCertScript(domain), false)
	if err != nil {
		fmt.Printf("WARN: сертификат для %s%s: %v\n%s\n", siteAliasPrefix, domain, err, strings.TrimSpace(out))
		return
	}
	fmt.Println(strings.TrimSpace(out))
}
