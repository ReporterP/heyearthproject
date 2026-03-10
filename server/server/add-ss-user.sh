#!/bin/bash
# Персональная ссылка Shadowsocks: 1 ID -> 1 SS URI.
# Вызов: /opt/vpn-bot/add-ss-user.sh INSTANCE [new]
# INSTANCE = telegram_id (число) или app user_id (строка a-z0-9_, например UUID с заменой - на _).
# Выводит в stdout одну строку — ss://... URL. При ошибке — в stderr и exit != 0.

set -e
TELEGRAM_ID="$1"
MODE="${2:-}"
[ -z "$TELEGRAM_ID" ] && echo "Usage: add-ss-user.sh TELEGRAM_ID [new]" >&2 && exit 1
SS_USERS="/opt/vpn-bot/ss-users.txt"
CLIENTS_DIR="/etc/shadowsocks-libev/clients"
SS_HOST="${SS_HOST:-heyearth.ru}"
SS_METHOD="${SS_METHOD:-chacha20-ietf-poly1305}"
PORT_START=8388

mkdir -p "$CLIENTS_DIR"
touch "$SS_USERS"

# Уже есть пользователь — вернуть существующую ссылку только если конфиг и сервис на месте
if [ -n "$TELEGRAM_ID" ] && [ "$MODE" != "new" ]; then
  while IFS= read -r line; do
    [ -z "$line" ] && continue
    id=$(echo "$line" | awk '{print $1}')
    port=$(echo "$line" | awk '{print $2}')
    pass=$(echo "$line" | awk '{print $3}')
    if [ "$id" = "$TELEGRAM_ID" ] && [ -n "$port" ] && [ -n "$pass" ]; then
      if [ -f "${CLIENTS_DIR}/${TELEGRAM_ID}.json" ] && systemctl is-active --quiet "shadowsocks-libev@${TELEGRAM_ID}.service" 2>/dev/null; then
        B64=$(echo -n "${SS_METHOD}:${pass}" | base64 | tr -d '\n')
        echo "ss://${B64}@${SS_HOST}:${port}#HeYearth-${TELEGRAM_ID}"
        exit 0
      fi
      # конфиг или сервис отсутствуют — отозвать старую запись и создать заново (как /newss)
      systemctl stop "shadowsocks-libev@${TELEGRAM_ID}.service" 2>/dev/null || true
      systemctl disable "shadowsocks-libev@${TELEGRAM_ID}.service" 2>/dev/null || true
      rm -f "${CLIENTS_DIR}/${TELEGRAM_ID}.json"
      grep -v "^${TELEGRAM_ID} " "$SS_USERS" > "${SS_USERS}.tmp" 2>/dev/null || true
      mv "${SS_USERS}.tmp" "$SS_USERS" 2>/dev/null || true
      break
    fi
  done < "$SS_USERS"
fi

# Режим new: отозвать старый
if [ "$MODE" = "new" ] && [ -n "$TELEGRAM_ID" ]; then
  systemctl stop "shadowsocks-libev@${TELEGRAM_ID}.service" 2>/dev/null || true
  systemctl disable "shadowsocks-libev@${TELEGRAM_ID}.service" 2>/dev/null || true
  rm -f "${CLIENTS_DIR}/${TELEGRAM_ID}.json"
  grep -v "^${TELEGRAM_ID} " "$SS_USERS" > "${SS_USERS}.tmp" 2>/dev/null || true
  mv "${SS_USERS}.tmp" "$SS_USERS" 2>/dev/null || true
fi

# Назначить следующий свободный порт
max_port=$PORT_START
while IFS= read -r line; do
  [ -z "$line" ] && continue
  p=$(echo "$line" | awk '{print $2}')
  [ -z "$p" ] && continue
  [ "$p" -ge "$max_port" ] 2>/dev/null && max_port=$((p + 1))
done < "$SS_USERS"
for f in "${CLIENTS_DIR}"/*.json; do
  [ -e "$f" ] || [ -L "$f" ] || continue
  p=$(grep -o '"server_port"[[:space:]]*:[[:space:]]*[0-9]*' "$f" 2>/dev/null | grep -o '[0-9]*')
  [ -z "$p" ] && continue
  [ "$p" -ge "$max_port" ] 2>/dev/null && max_port=$((p + 1))
done
PORT=$max_port
PASSWORD=$(openssl rand -base64 24 | tr -d '\n/+=' | head -c 24)

# Конфиг для одного пользователя
cat > "${CLIENTS_DIR}/${TELEGRAM_ID}.json" << EOF
{
    "server": ["::0", "0.0.0.0"],
    "server_port": ${PORT},
    "password": "${PASSWORD}",
    "method": "${SS_METHOD}",
    "mode": "tcp_and_udp",
    "timeout": 60
}
EOF
chmod 600 "${CLIENTS_DIR}/${TELEGRAM_ID}.json"

echo "${TELEGRAM_ID} ${PORT} ${PASSWORD}" >> "$SS_USERS"
systemctl enable "shadowsocks-libev@${TELEGRAM_ID}.service" 2>/dev/null || true
systemctl start "shadowsocks-libev@${TELEGRAM_ID}.service" 2>/dev/null || true

B64=$(echo -n "${SS_METHOD}:${PASSWORD}" | base64 | tr -d '\n')
echo "ss://${B64}@${SS_HOST}:${PORT}#HeYearth-${TELEGRAM_ID}"
