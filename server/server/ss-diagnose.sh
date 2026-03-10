#!/bin/bash
# Диагностика Shadowsocks на сервере (почему интернет через SS может не работать).
# Запуск: sudo bash ss-diagnose.sh

echo "=== Shadowsocks: сервис ==="
systemctl is-active shadowsocks-libev 2>/dev/null || echo "не установлен/не активен"
systemctl status shadowsocks-libev --no-pager 2>/dev/null | head -15

echo ""
echo "=== Порты SS (8388–8488) — слушают? ==="
ss -tulnp 2>/dev/null | grep -E '838[0-9]|84[0-7][0-9]' || echo "Порты 8388–8488 не слушаются (персональные инстансы shadowsocks-libev@ID)"

echo ""
echo "=== iptables INPUT (порты 8388–8488) ==="
iptables -L INPUT -n -v 2>/dev/null | head -25

echo ""
echo "=== Персональные инстансы (clients) ==="
if [ -d /etc/shadowsocks-libev/clients ]; then
  ls -la /etc/shadowsocks-libev/clients/*.json 2>/dev/null || echo "Нет конфигов в clients/"
  echo "Регистрация пользователей: /opt/vpn-bot/ss-users.txt"
else
  echo "Каталог clients не найден (используется add-ss-user.sh)."
fi

echo ""
echo "=== Рекомендации ==="
echo "1. Персональные ссылки: бот/API вызывают add-ss-user.sh; инстансы — systemctl status 'shadowsocks-libev@*'."
echo "2. Если порты 8388–8488 не в LISTEN — проверь: systemctl list-units 'shadowsocks-libev@*'."
echo "3. Если в iptables нет ACCEPT для диапазона — добавь: iptables -A INPUT -p tcp --dport 8388:8488 -j ACCEPT; iptables -A INPUT -p udp --dport 8388:8488 -j ACCEPT"
echo "4. В клиенте обязательно включи «глобальный режим» / System Proxy, иначе трафик не идёт через прокси."
