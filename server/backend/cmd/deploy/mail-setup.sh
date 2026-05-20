#!/usr/bin/env bash
# Mailcow installation script for a dedicated VPS.
#
# Run on a FRESH Ubuntu 22.04/24.04 server.
# Pre-reqs:
#   - root or sudo access
#   - DNS A record mail.hiearth.ru → <this VPS IP> already created
#     (other DNS records — MX, SPF, DKIM, DMARC, PTR — added after install)
#
# Usage:  bash mail-setup.sh <mail-fqdn>
# Example: bash mail-setup.sh mail.hiearth.ru

set -euo pipefail

MAIL_HOSTNAME="${1:-mail.hiearth.ru}"
MAILCOW_DIR="/opt/mailcow-dockerized"
TZ_DEFAULT="Europe/Moscow"

if [[ "$(id -u)" -ne 0 ]]; then
  echo "ERROR: run as root (sudo bash $0 ...)" >&2
  exit 1
fi

echo "=== Mailcow setup for ${MAIL_HOSTNAME} ==="

# ── 1. System hygiene ──────────────────────────────────────────────────────
echo
echo "[1/7] Updating system + base packages..."
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get -y upgrade
apt-get -y install \
  curl \
  git \
  htop \
  ca-certificates \
  ufw \
  unattended-upgrades \
  jq \
  bash-completion

# Hostname for outbound SMTP HELO and TLS cert SAN.
hostnamectl set-hostname "${MAIL_HOSTNAME}"
echo "${MAIL_HOSTNAME}" > /etc/hostname

# Make sure /etc/hosts maps our public name to localhost so Mailcow's HELO
# doesn't try to resolve it externally during boot.
if ! grep -q "${MAIL_HOSTNAME}" /etc/hosts; then
  echo "127.0.1.1 ${MAIL_HOSTNAME}" >> /etc/hosts
fi

# ── 2. Docker (official repo, not the snap version) ────────────────────────
echo
echo "[2/7] Installing Docker engine + compose plugin..."
if ! command -v docker >/dev/null 2>&1; then
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg \
    | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
  chmod a+r /etc/apt/keyrings/docker.gpg
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] \
    https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" \
    > /etc/apt/sources.list.d/docker.list
  apt-get update -qq
  apt-get -y install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
  systemctl enable --now docker
fi
docker --version
docker compose version

# ── 3. Firewall ────────────────────────────────────────────────────────────
# All ports that any mail client (or other mail server) might dial.
# Mailcow itself runs Postfix on 25/465/587, Dovecot on 143/993/110/995,
# admin/webmail behind nginx on 80/443, and ManageSieve on 4190.
echo
echo "[3/7] Configuring UFW firewall..."
ufw --force reset >/dev/null
ufw default deny incoming
ufw default allow outgoing
ufw allow 22/tcp     comment 'SSH'
ufw allow 25/tcp     comment 'SMTP inbound (MTA-to-MTA)'
ufw allow 80/tcp     comment 'HTTP (ACME challenge + redirect)'
ufw allow 443/tcp    comment 'HTTPS (admin + webmail)'
ufw allow 465/tcp    comment 'SMTPS (implicit TLS submission)'
ufw allow 587/tcp    comment 'Submission (STARTTLS submission)'
ufw allow 143/tcp    comment 'IMAP (STARTTLS)'
ufw allow 993/tcp    comment 'IMAPS (implicit TLS)'
ufw allow 110/tcp    comment 'POP3 (STARTTLS)'
ufw allow 995/tcp    comment 'POP3S (implicit TLS)'
ufw allow 4190/tcp   comment 'ManageSieve'
ufw --force enable
ufw status numbered

# ── 4. Clone Mailcow ───────────────────────────────────────────────────────
echo
echo "[4/7] Cloning Mailcow-dockerized..."
umask 0022
if [[ ! -d "${MAILCOW_DIR}" ]]; then
  git clone https://github.com/mailcow/mailcow-dockerized "${MAILCOW_DIR}"
fi
cd "${MAILCOW_DIR}"
# Stay on the released branch — master is unstable in places.
git pull --ff-only || true

# ── 5. Generate config ─────────────────────────────────────────────────────
# generate_config.sh is interactive by default. Pre-feed answers via env so
# this script can run unattended:
#   MAILCOW_HOSTNAME → FQDN that the mail server reports
#   MAILCOW_TZ       → server timezone
echo
echo "[5/7] Generating mailcow.conf for ${MAIL_HOSTNAME}..."
if [[ ! -f mailcow.conf ]]; then
  MAILCOW_HOSTNAME="${MAIL_HOSTNAME}" MAILCOW_TZ="${TZ_DEFAULT}" \
    ./generate_config.sh <<EOF
${MAIL_HOSTNAME}
${TZ_DEFAULT}
EOF
else
  echo "  (mailcow.conf already exists, keeping)"
fi

# Default admin password is `moohoo` — emit a banner so the operator knows
# to change it immediately after first login.
echo
echo "=== !!! DEFAULT ADMIN PASSWORD IS 'moohoo' — CHANGE ON FIRST LOGIN !!! ==="
echo

# ── 6. Pull + start containers ─────────────────────────────────────────────
echo "[6/7] Pulling images and starting Mailcow (this takes a few minutes)..."
docker compose pull
docker compose up -d
sleep 10
docker compose ps

# ── 7. Final summary ───────────────────────────────────────────────────────
PUBLIC_IP="$(curl -fsSL https://api.ipify.org || hostname -I | awk '{print $1}')"
cat <<EOF

═══════════════════════════════════════════════════════════════════════════
 ✓ Mailcow is up. Next steps:

 1. Open the admin UI:
      https://${MAIL_HOSTNAME}/
    Login: admin / moohoo  ← CHANGE IMMEDIATELY (Configuration → Administrators).

 2. Add a domain:
      Configuration → Mail Setup → Domains → + Add domain
      Domain: hiearth.ru
      Mailboxes: 50, Aliases: 400 (defaults are fine)

 3. Add mailboxes (after adding the domain):
      noreply@hiearth.ru   ← used by hiearth-api for transactional mail
      support@hiearth.ru   ← for client communication
      alex@hiearth.ru      ← personal
      dmarc@hiearth.ru     ← receives DMARC reports

 4. Generate DKIM key (Configuration → Mail Setup → ARC/DKIM keys → + Add key):
      Selector: dkim
      Domain:   hiearth.ru
      Key size: 2048

    Copy the public key value (starts "v=DKIM1; k=rsa; p=...") and paste it
    as a TXT record at  dkim._domainkey.hiearth.ru .

 5. Add the rest of the DNS records at your registrar (see DNS table):
      A     mail.hiearth.ru → ${PUBLIC_IP}
      MX    hiearth.ru       → 10 mail.hiearth.ru
      TXT   hiearth.ru       → "v=spf1 mx -all"
      TXT   _dmarc.hiearth.ru → "v=DMARC1; p=quarantine; rua=mailto:dmarc@hiearth.ru"
      PTR   ${PUBLIC_IP}     → mail.hiearth.ru   ← ask your VPS provider

 6. Verify end-to-end:
      a) Open https://www.mail-tester.com/  in a browser, copy the test
         address shown there.
      b) From the webmail at https://${MAIL_HOSTNAME}/ send a test message
         to that address.
      c) Click "Then check your score" on mail-tester — aim for ≥ 9/10.
         Anything <8 means a DNS record is missing or wrong.

 7. Once you're at 10/10, paste these SMTP credentials in the admin panel
    at https://admin.hiearth.ru/settings :
      smtp_host        mail.hiearth.ru
      smtp_port        587
      smtp_use_tls     true
      smtp_user        noreply@hiearth.ru
      smtp_password    <password you set when creating the mailbox>
      smtp_from_email  noreply@hiearth.ru
      smtp_from_name   HiEarth
      smtp_enabled     true

 Mail-tester URL again:  https://www.mail-tester.com/
 Mailcow docs:           https://docs.mailcow.email/
═══════════════════════════════════════════════════════════════════════════
EOF
