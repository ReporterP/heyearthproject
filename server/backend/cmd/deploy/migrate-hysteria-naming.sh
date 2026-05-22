#!/usr/bin/env bash
# One-time migration: rename legacy hysteria2-client.service → hysteria2-client-<id>.service.
# Idempotent: if no legacy unit exists, exits 0.
# Run ON the hub.

set -euo pipefail

LEGACY_UNIT="/etc/systemd/system/hysteria2-client.service"
HYSTERIA_DIR="/opt/heyearth/hysteria"
BACKUP_DIR="/opt/heyearth/state/legacy-hysteria-migration-$(date +%Y%m%d-%H%M%S)"

if [[ ! -f "$LEGACY_UNIT" ]]; then
    echo "[migrate-hysteria-naming] no legacy unit at $LEGACY_UNIT — nothing to do"
    exit 0
fi

# Identify which exit-node the legacy unit points to via the config it runs.
LEGACY_CONFIG="$HYSTERIA_DIR/client.yaml"
if [[ ! -f "$LEGACY_CONFIG" ]]; then
    echo "[migrate-hysteria-naming] legacy unit found but $LEGACY_CONFIG missing — manual fix required" >&2
    exit 1
fi

EXIT_IP=$(grep -E '^server:' "$LEGACY_CONFIG" | awk '{print $2}' | cut -d: -f1)
if [[ -z "$EXIT_IP" ]]; then
    echo "[migrate-hysteria-naming] could not parse server IP from $LEGACY_CONFIG" >&2
    exit 1
fi
echo "[migrate-hysteria-naming] legacy unit points to $EXIT_IP"

# Look up server id from DB.
SERVER_ID=$(sudo -u postgres psql -tA heyearth -c "SELECT id FROM servers WHERE ip_address='$EXIT_IP' AND is_exit_node=true LIMIT 1;")
SERVER_ID=$(echo "$SERVER_ID" | tr -d ' \n')
if [[ -z "$SERVER_ID" || ! "$SERVER_ID" =~ ^[0-9]+$ ]]; then
    echo "[migrate-hysteria-naming] could not find server id for $EXIT_IP" >&2
    exit 1
fi
echo "[migrate-hysteria-naming] mapped to server id=$SERVER_ID"

NEW_UNIT="/etc/systemd/system/hysteria2-client-${SERVER_ID}.service"
NEW_CONFIG="$HYSTERIA_DIR/client-${SERVER_ID}.yaml"

if [[ -f "$NEW_UNIT" ]]; then
    echo "[migrate-hysteria-naming] $NEW_UNIT already exists — assuming migration done; stopping legacy + cleaning up"
    systemctl stop hysteria2-client 2>/dev/null || true
    systemctl disable hysteria2-client 2>/dev/null || true
    mkdir -p "$BACKUP_DIR"
    mv "$LEGACY_UNIT" "$BACKUP_DIR/" 2>/dev/null || true
    mv "$LEGACY_CONFIG" "$BACKUP_DIR/" 2>/dev/null || true
    systemctl daemon-reload
    echo "[migrate-hysteria-naming] legacy artefacts archived to $BACKUP_DIR"
    exit 0
fi

mkdir -p "$BACKUP_DIR"
cp "$LEGACY_UNIT"   "$BACKUP_DIR/hysteria2-client.service.before-migration"
cp "$LEGACY_CONFIG" "$BACKUP_DIR/client.yaml.before-migration"
echo "[migrate-hysteria-naming] backups in $BACKUP_DIR"

systemctl stop hysteria2-client
systemctl disable hysteria2-client

sed "s|client\\.yaml|client-${SERVER_ID}.yaml|g; s|Hysteria2 VPN Client.*|Hysteria2 Client for server id=${SERVER_ID} (${EXIT_IP}) — HeYearth|" \
    "$LEGACY_UNIT" > "$NEW_UNIT"
mv "$LEGACY_CONFIG" "$NEW_CONFIG"
rm "$LEGACY_UNIT"

systemctl daemon-reload
systemctl enable "hysteria2-client-${SERVER_ID}"
systemctl restart "hysteria2-client-${SERVER_ID}"
sleep 2
systemctl is-active "hysteria2-client-${SERVER_ID}" >/dev/null && \
    echo "[migrate-hysteria-naming] hysteria2-client-${SERVER_ID} is ACTIVE ✓" || {
        echo "[migrate-hysteria-naming] new unit did not start; restoring legacy"
        cp "$BACKUP_DIR/hysteria2-client.service.before-migration" "$LEGACY_UNIT"
        cp "$BACKUP_DIR/client.yaml.before-migration"             "$LEGACY_CONFIG"
        systemctl daemon-reload
        systemctl enable hysteria2-client
        systemctl restart hysteria2-client
        rm -f "$NEW_UNIT" "$NEW_CONFIG"
        exit 1
    }

echo "[migrate-hysteria-naming] DONE. Legacy at $BACKUP_DIR"
