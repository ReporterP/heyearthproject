# Анализ: почему через VPN «висит» весь интернет

Когда при подключении к VPN весь трафик фактически перестаёт работать, причина почти всегда на **сервере** — ядро или файрвол отбрасывают пакеты.

---

## 1. Reverse Path Filter (rp_filter) — самая частая причина

**Что происходит:** Ответы из интернета приходят на внешний интерфейс сервера с адресом назначения 10.66.66.x (клиент VPN). Ядро проверяет: «по своей таблице маршрутов пакет с dst=10.66.66.x ушёл бы в wg0, а пришёл на eth0» — и считает это подозрительным, **отбрасывает пакет**. В итоге запросы уходят (NAT), а ответы дропаются → соединения висят по таймауту.

**Исправление на сервере:**
```bash
# В /etc/sysctl.conf добавить или изменить:
net.ipv4.conf.all.rp_filter=0
net.ipv4.conf.default.rp_filter=0
# Применить:
sysctl -p
```

В `setup-wireguard.sh` и `tune-server.sh` это уже добавлено.

---

## 2. Фрагментация и MTU / TCP MSS

**Что происходит:** По туннелю пакеты «упаковываются», эффективный MTU меньше. Если TCP отправляет сегменты больше этого размера, они режутся, фрагменты могут теряться → соединения подвисают или обрываются.

**Исправление:**
- В конфигах клиентов уже стоит **MTU = 1280**.
- На сервере в PostUp добавлено **TCP MSS clamping**: `iptables -t mangle -A FORWARD -i %i -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu` — так TCP сам подстраивает размер сегмента под путь.

Для уже работающего сервера: залить обновлённый `setup-wireguard.sh` не всегда удобно, поэтому в `tune-server.sh` добавлены только sysctl (в т.ч. rp_filter). Полный фикс (включая MSS) даёт новая установка по `setup-wireguard.sh` или ручное добавление этой строки в PostUp в `/etc/wireguard/wg0.conf` и перезапуск `wg-quick@wg0`.

---

## 3. NAT (MASQUERADE) и FORWARD

**Что происходит:** Трафик клиента (10.66.66.x) должен уходить в интернет от имени сервера и возвращаться обратно. Для этого нужны:
- `net.ipv4.ip_forward=1`
- правила iptables: FORWARD для интерфейса туннеля и NAT (POSTROUTING, MASQUERADE).

В нашем скрипте это есть. Если на сервере жёсткий файрвол (ufw/iptables), он не должен перекрывать FORWARD для wg0 и исходящий NAT.

---

## 4. DNS

**Что происходит:** Если DNS-запросы не доходят до 1.1.1.1/8.8.8.8 или не возвращаются, приложения ждут разрешения имён и кажется, что «всё висит».

**Проверка на сервере:**  
`curl -s 8.8.8.8` или `dig @8.8.8.8 google.com` — должен быть ответ. В конфиге клиента уже прописаны DNS; при необходимости их можно сменить (переменная WG_DNS в add-client.sh или вручную в конфиге).

---

## 5. Что сделать на уже работающем сервере

1. **Применить настройки из tune-server.sh** (в т.ч. rp_filter):
   ```bash
   sed -i 's/\r$//' /tmp/tune-server.sh
   bash /tmp/tune-server.sh
   ```

2. **Вручную включить rp_filter, если скрипт не трогал sysctl:**
   ```bash
   echo "net.ipv4.conf.all.rp_filter=0" >> /etc/sysctl.conf
   echo "net.ipv4.conf.default.rp_filter=0" >> /etc/sysctl.conf
   sysctl -p
   ```

3. **Добавить TCP MSS clamping в wg0 и перезапустить** — одной командой на сервере:
   ```bash
   WG_CONF="/etc/wireguard/wg0.conf"; if [ -f "$WG_CONF" ] && ! grep -q 'TCPMSS' "$WG_CONF"; then sed -i '/^PostUp = /a PostUp = iptables -t mangle -A FORWARD -i %i -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu' "$WG_CONF"; sed -i '/^PostDown = /a PostDown = iptables -t mangle -D FORWARD -i %i -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu' "$WG_CONF"; fi; systemctl restart wg-quick@wg0
   ```
   Или просто запустить `tune-server.sh` — он сам добавит MSS и перезапустит wg0.

4. Убедиться, что **исходящий доступ с сервера** в интернет есть (ping 8.8.8.8, curl https://google.com).

---

## Кратко

| Причина              | Симптом              | Решение |
|----------------------|----------------------|--------|
| rp_filter=1          | Ответы дропаются    | rp_filter=0 в sysctl |
| Большой MTU / нет MSS| Обрывы, зависания   | MTU 1280 у клиента + TCPMSS clamp на сервере |
| Нет ip_forward       | Трафик не идёт      | net.ipv4.ip_forward=1 |
| DNS не ходит         | «Всё висит»         | Проверить доступ к 8.8.8.8/1.1.1.1, при необходимости сменить DNS в конфиге |

После правок на сервере клиенту достаточно переподключиться к VPN (при необходимости взять новый конфиг через /newconfig).
