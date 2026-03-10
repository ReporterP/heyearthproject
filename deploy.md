# Деплой VPN + Telegram-бота на VPS

**Актуальная инструкция:** см. **ИНСТРУКЦИЯ_POWERSHELL.md** (Shadowsocks, Go-деплой). Ниже — устаревшее описание (WireGuard, Python-бот).

Домен **heyearth.ru** используется как endpoint в конфигах клиентов — при смене IP сервера достаточно обновить DNS, конфиги переподключаться не нужно.

## 1. Подключение к серверу

```bash
ssh root@155.212.225.172
```

## 2. Установка WireGuard

Скопируй на сервер папку `server/` (или вставь содержимое скриптов вручную).

На сервере:

```bash
chmod +x /root/setup-wireguard.sh
/root/setup-wireguard.sh
```

После установки скопируй `add-client.sh` в `/etc/wireguard/` и сделай исполняемым:

```bash
cp /root/add-client.sh /etc/wireguard/
chmod +x /etc/wireguard/add-client.sh
```

## 3. Деплой бота

На сервере:

```bash
mkdir -p /opt/vpn-bot
# Скопируй с ПК в /opt/vpn-bot: bot.py, config.py, requirements.txt
pip3 install -r /opt/vpn-bot/requirements.txt
```

Создай файл с токеном:

```bash
echo 'TELEGRAM_BOT_TOKEN=ТВОЙ_ТОКЕН_ОТ_BOTFATHER' > /opt/vpn-bot/.env
```

Установи и включи systemd-сервис:

```bash
cp /opt/vpn-bot/telegram-bot.service /etc/systemd/system/
# Отредактируй и вставь токен: nano /etc/systemd/system/telegram-bot.service
systemctl daemon-reload
systemctl enable telegram-bot
systemctl start telegram-bot
systemctl status telegram-bot
```

Логи: `journalctl -u telegram-bot -f`

## 4. Мини-сайт (опционально)

В папке `site/` лежит одностраничный сайт со ссылкой на бота. Чтобы повесить его на heyearth.ru:

1. В `site/index.html` замени `HeYearthVPNBot` в ссылке на username своего бота.
2. Скопируй содержимое `site/` на сервер в каталог, отдаваемый nginx/apache (например `/var/www/html/` или свой виртуальный хост).
3. Настрой веб-сервер на домен heyearth.ru.

## 5. Проверка

- В Telegram: найди бота, /start → /register → /getconfig. Должен прийти файл `.conf`.
- Инструкция по установке VPN на устройства: в боте команда /instruction.
- Установи приложение WireGuard на телефон/ПК, импортируй конфиг и подключись.
