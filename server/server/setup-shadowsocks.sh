#!/bin/bash
# Установка Shadowsocks-libev на VPS: персональная ссылка на пользователя (add-ss-user.sh).
# Запуск: sudo ./setup-shadowsocks.sh
# Каждый пользователь получает свой порт и пароль через бота (/getss, /newss).

set -e
SS_HOST="${SS_HOST:-heyearth.ru}"
CLIENTS_DIR="/etc/shadowsocks-libev/clients"
SS_USERS="/opt/vpn-bot/ss-users.txt"
PORT_FIRST=8388
PORT_LAST=8488

echo "=== Установка Shadowsocks-libev ==="
apt-get update
apt-get install -y shadowsocks-libev

echo "=== Каталог клиентов и учёт пользователей ==="
mkdir -p "$CLIENTS_DIR"
mkdir -p /opt/vpn-bot
touch "$SS_USERS"

echo "=== Template unit для per-user (shadowsocks-libev@telegram_id) ==="
cat > /etc/systemd/system/shadowsocks-libev@.service << 'SYSTEMD'
[Unit]
Description=Shadowsocks-libev per-user %i
After=network.target

[Service]
Type=simple
User=root
ExecStart=/usr/bin/ss-server -c /etc/shadowsocks-libev/clients/%i.json
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
SYSTEMD
systemctl daemon-reload

# Дефолтный сервис не используем (персональные инстансы через template)
systemctl stop shadowsocks-libev.service 2>/dev/null || true
systemctl disable shadowsocks-libev.service 2>/dev/null || true

echo "=== Открытие портов ${PORT_FIRST}-${PORT_LAST} в файрволе ==="
iptables -A INPUT -p tcp --dport ${PORT_FIRST}:${PORT_LAST} -j ACCEPT 2>/dev/null || true
iptables -A INPUT -p udp --dport ${PORT_FIRST}:${PORT_LAST} -j ACCEPT 2>/dev/null || true
if command -v ufw >/dev/null 2>&1; then
  ufw allow ${PORT_FIRST}:${PORT_LAST}/tcp 2>/dev/null || true
  ufw allow ${PORT_FIRST}:${PORT_LAST}/udp 2>/dev/null || true
  ufw status | head -25 || true
fi

echo "=== Готово ==="
echo "Персональные ссылки выдаёт бот: /getss (получить), /newss (отозвать и выдать новую)."
