# Полная инструкция: деплой VPN (Shadowsocks) и бота через PowerShell

Сервер: **155.212.225.172**, пользователь **root**, пароль у тебя. Домен: **heyearth.ru**.  
Используется только **Shadowsocks** (персональные ссылки). WireGuard не устанавливается.

Конфиг деплоя: в корне проекта скопируй **.env.example** в **.env** и заполни (DEPLOY_SSH_PASSWORD, TELEGRAM_BOT_TOKEN, при необходимости LETSENCRYPT_EMAIL, SITE_DOMAIN).

---

## Твои шаги (кратко)

### Если делаешь всё с нуля или хочешь «обновить всё разом»

1. **На ПК:** открой PowerShell.
2. **Перейди в папку проекта:**
   ```powershell
   cd "C:\Users\alex\OneDrive\Рабочий стол\heyearthproject"
   ```
3. **Проверь файл `.env`** в этой папке: в нём должны быть `DEPLOY_SSH_PASSWORD`, `TELEGRAM_BOT_TOKEN`, при желании `SITE_DOMAIN=heyearth.ru`. Если `.env` нет — скопируй из `.env.example` и заполни.
4. **Установи Go** (если ещё нет): https://go.dev/dl/ — нужен для сборки и деплоя.
5. **Запусти деплой** (из корня проекта):
   - **Первый раз на сервере (или полная переустановка):**  
     `go run ./backend/cmd/deploy --fresh`  
     На сервер установится Go, бот, Shadowsocks (персональные ссылки), API и сайт.
   - **Обновить файлы и перезапустить сервисы:**  
     `go run ./backend/cmd/deploy --update`
   - **Только перезапустить сервисы:**  
     `go run ./backend/cmd/deploy --restart`
   - **Только обновить сайт и SSL:**  
     `go run ./backend/cmd/deploy --site`  
   Альтернатива: `python scripts/deploy.py --fresh` (тот же Go-деплой через Python).
6. После `--fresh` или `--update` зайди в бота в Telegram: `/start` → `/register` → `/getss` (получить ссылку), при необходимости `/newss` (новая ссылка). Сайт: **https://heyearth.ru** (если в `.env` указан `LETSENCRYPT_EMAIL` и ты запускал деплой с сайтом).

### Если интернет не работает через Shadowsocks

1. **В клиенте (Outline, Shadowsocks и т.п.):** после добавления сервера по ссылке из бота обязательно включи **глобальный режим** (System Proxy / «Проксировать весь трафик»). Без этого трафик не идёт через прокси.
2. **На сервере:** по SSH выполни `bash /root/ss-diagnose.sh` — покажет, слушаются ли порты SS. Для персональных инстансов открыт диапазон 8388–8488. Если порты закрыты, открой вручную:
   ```bash
   iptables -A INPUT -p tcp --dport 8388:8488 -j ACCEPT
   iptables -A INPUT -p udp --dport 8388:8488 -j ACCEPT
   ```

### Если Shadowsocks на сервере ещё не ставился

При `--fresh` он ставится сам. Если деплоил раньше без SS — залей скрипт и запусти на сервере (см. раздел «Shadowsocks» ниже в инструкции).

### Если в приложении «Сервер недоступен»

1. В браузере открой **https://heyearth.ru** — если не открывается по HTTPS, в `.env` задай `LETSENCRYPT_EMAIL=твой@email` и выполни `python scripts/deploy.py --site`.
2. На сервере по SSH: `systemctl status heyearth-api` — должен быть **active**. Если нет — `journalctl -u heyearth-api -n 30`.
3. В приложении нажми «Проверить» у адреса сервера — после настройки HTTPS и запущенного API должно быть «Сервер доступен».

---

**Если хостинг Beget и порт 22 не отвечает:** у Beget SSH по умолчанию выключен. Зайди в [панель](https://cp.beget.com) → главная страница → включи SSH в меню слева. Для **виртуального хостинга** в DEPLOY_HOST укажи **имя сервера** из таблицы «Общая информация» (например, `логин.beget.tech`), а не IP сайта; логин и пароль — от входа в панель. Для **VPS** реквизиты смотри в разделе «Облако» (root@IP). Порт у Beget обычно 22.

**Режимы деплоя (Go):**
- `go run ./backend/cmd/deploy --fresh` — новый сервер, полная установка (+ установка Go на сервер)
- `go run ./backend/cmd/deploy --update` — обновить файлы и перезапустить сервисы
- `go run ./backend/cmd/deploy --restart` — только перезапуск бота, API и nginx
- `go run ./backend/cmd/deploy --site` — только сайт и SSL

---

## Вариант А: Автоматический деплой (один скрипт)

Делается всё с твоего ПК одной командой.

### 1. Открой PowerShell

(Правый клик по кнопке Пуск → Windows PowerShell, или Win+X → «Терминал».)

### 2. Перейди в папку проекта

```powershell
cd "C:\Users\alex\OneDrive\Рабочий стол\heyearthproject"
```

### 3. Установи Go (один раз)

Скачай с https://go.dev/dl/ и установи. Проверка: `go version`.

### 4. Запусти деплой

Создай в корне проекта файл `.env` (скопируй из `.env.example`), пропиши `DEPLOY_SSH_PASSWORD` и `TELEGRAM_BOT_TOKEN`. Затем:

```powershell
go run ./backend/cmd/deploy --fresh
```

Скрипт сам (при --fresh также установит Go на сервер):
- подключится к серверу по SSH;
- зальёт бота и `add-ss-user.sh` в `/opt/vpn-bot/`;
- установит Shadowsocks (персональные ссылки по /getss, /newss в боте);
- создаст и запустит systemd-сервис бота;
- развернёт сайт.

Если появятся ошибки — скопируй вывод и пришли.

---

## Вариант Б: Ручной деплой по шагам

Если автоматический не сработает или хочешь делать всё вручную.

### Шаг 1. Перейти в папку проекта в PowerShell

```powershell
cd "C:\Users\alex\OneDrive\Рабочий стол\heyearthproject"
```

### Шаг 2. Подключиться к серверу по SSH

```powershell
ssh root@155.212.225.172
```

Введи пароль, когда попросит. Должна открыться консоль сервера (приглашение вида `root@...`).

### Шаг 3. Установить Shadowsocks (вручную) или использовать деплой

**Проще всего:** выйди с сервера (`exit`) и с ПК выполни `go run ./backend/cmd/deploy --fresh` — установится Go на сервер, Shadowsocks, бот и API. Затем переходи к шагу 9 (Проверка).

**Если делаешь вручную:** выйди с сервера (`exit`). В PowerShell скопируй скрипт и зайди снова по SSH:

```powershell
scp "C:\Users\alex\OneDrive\Рабочий стол\heyearthproject\server\setup-shadowsocks.sh" root@155.212.225.172:/root/
ssh root@155.212.225.172
```

На сервере: `sed -i 's/\r$//' /root/setup-shadowsocks.sh && chmod +x /root/setup-shadowsocks.sh && SS_HOST=heyearth.ru /root/setup-shadowsocks.sh`. Затем `exit`.

### Шаг 4. Скопировать файлы бота на сервер

В PowerShell на ПК:

```powershell
ssh root@155.212.225.172 "mkdir -p /opt/vpn-bot"
scp "C:\Users\alex\OneDrive\Рабочий стол\heyearthproject\bot\bot.py" root@155.212.225.172:/opt/vpn-bot/
scp "C:\Users\alex\OneDrive\Рабочий стол\heyearthproject\bot\config.py" root@155.212.225.172:/opt/vpn-bot/
scp "C:\Users\alex\OneDrive\Рабочий стол\heyearthproject\bot\requirements.txt" root@155.212.225.172:/opt/vpn-bot/
scp "C:\Users\alex\OneDrive\Рабочий стол\heyearthproject\server\add-ss-user.sh" root@155.212.225.172:/opt/vpn-bot/
```

Если папки `/opt/vpn-bot` на сервере ещё не было, она создаётся первой командой. На сервере потом выполни: `chmod +x /opt/vpn-bot/add-ss-user.sh`

### Шаг 5. Зайти на сервер и настроить бота

```powershell
ssh root@155.212.225.172
```

На сервере:

**5.1. Создать файл с токеном бота**

Подставь свой токен от BotFather вместо `ТВОЙ_ТОКЕН`:

```bash
echo 'TELEGRAM_BOT_TOKEN=ТВОЙ_ТОКЕН' > /opt/vpn-bot/.env
```

Или открой файл и вставь строку вручную:

```bash
nano /opt/vpn-bot/.env
```

Внутри должна быть одна строка:

```
TELEGRAM_BOT_TOKEN=8739993462:AAGQ1nIiV70AhG1BRd2pBNh6lLcNgGuV7P4
```

Сохранить: Ctrl+O, Enter, выйти: Ctrl+X.

**5.2. Установить зависимости Python (через venv — на Ubuntu 24+ иначе pip выдаст externally-managed-environment)**

```bash
apt-get install -y python3 python3-venv
python3 -m venv /opt/vpn-bot/venv
/opt/vpn-bot/venv/bin/pip install -r /opt/vpn-bot/requirements.txt
```

**5.3. Скопировать unit systemd и подставить токен**

С ПК можно скопировать готовый unit. На ПК в PowerShell (вторая сессия или после `exit`):

```powershell
scp "C:\Users\alex\OneDrive\Рабочий стол\heyearthproject\server\telegram-bot.service" root@155.212.225.172:/etc/systemd/system/telegram-bot.service
```

На сервере отредактировать файл и заменить **ВСТАВЬ_ТОКЕН** на свой токен:

```bash
nano /etc/systemd/system/telegram-bot.service
```

Или создать файл сразу с нужным содержимым (подставь свой токен в `ТВОЙ_ТОКЕН`):

```bash
cat > /etc/systemd/system/telegram-bot.service << 'EOF'
[Unit]
Description=Telegram VPN Bot (Shadowsocks)
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=/opt/vpn-bot
ExecStart=/opt/vpn-bot/venv/bin/python -u /opt/vpn-bot/bot.py
Restart=always
RestartSec=5
Environment=TELEGRAM_BOT_TOKEN=ТВОЙ_ТОКЕН
Environment=ADD_SS_USER_SCRIPT=/opt/vpn-bot/add-ss-user.sh
Environment=SS_HOST=heyearth.ru

[Install]
WantedBy=multi-user.target
EOF
```

Замени `ТВОЙ_ТОКЕН` на реальный токен (тот же, что в `.env`).

**5.4. Включить и запустить бота**

```bash
systemctl daemon-reload
systemctl enable telegram-bot
systemctl start telegram-bot
systemctl status telegram-bot
```

В статусе должно быть `active (running)`. Выйти с сервера: `exit`.

### Шаг 6. Проверка

1. В Telegram: `/start` → `/register` → `/getss`. Должна прийти персональная ссылка ss://.
2. Установи клиент (Outline, Shadowsocks, Shadowrocket), вставь ссылку и включи глобальный режим и «DNS через прокси».

### Shadowsocks

Зарегистрированные пользователи получают персональную ссылку по `/getss`, новую по `/newss`. Если бот пишет «Shadowsocks не настроен», установи на сервере вручную:

```powershell
scp "C:\Users\alex\OneDrive\Рабочий стол\heyearthproject\server\setup-shadowsocks.sh" root@155.212.225.172:/root/
```

По SSH на сервере (сначала убрать CRLF, если скопировалось с Windows):

```bash
sed -i 's/\r$//' /root/setup-shadowsocks.sh
chmod +x /root/setup-shadowsocks.sh
SS_HOST=heyearth.ru /root/setup-shadowsocks.sh
```

После этого бот будет выдавать персональные ссылки по `/getss` и `/newss`. Клиенты: Outline, Shadowsocks, Shadowrocket (iOS) и др.

*Важно:* в клиенте (Outline, Shadowsocks и т.д.) после добавления сервера нужно **включить глобальный режим** (System Proxy / «Проксировать весь трафик»). Иначе приложение подключается к серверу, но трафик в интернет не идёт через прокси — кажется, что «интернет не работает».

**Если через Shadowsocks интернет всё равно не работает:** на сервере выполни диагностику (скрипт заливается при деплое в `/root/`):
```bash
bash /root/ss-diagnose.sh
```
По выводу проверь: слушаются ли порты SS (8388–8488), есть ли правило iptables ACCEPT для диапазона. При необходимости открой порты: `iptables -A INPUT -p tcp --dport 8388:8488 -j ACCEPT; iptables -A INPUT -p udp --dport 8388:8488 -j ACCEPT` (см. [gvozdb.ru/linux/shadowsocks](https://gvozdb.ru/linux/shadowsocks)).

---

## Полезные команды на сервере

- Логи бота в реальном времени:  
  `journalctl -u telegram-bot -f`
- Перезапуск бота:  
  `systemctl restart telegram-bot`
- Перезапуск инстанса Shadowsocks пользователя (если нужно):  
  `systemctl restart shadowsocks-libev@TELEGRAM_ID.service`

---

## Shadowsocks: диагностика

Если ссылка из бота не работает, на сервере выполни: `bash /root/ss-diagnose.sh` — покажет статус сервиса и портов.

**Сайт не открывается:** убедись, что DNS для heyearth.ru указывает на IP сервера (155.212.225.172), nginx запущен (`systemctl status nginx`), порты 80 и 443 не закрыты файрволом.

---

## Если что-то пошло не так

- **«Connection refused» / таймаут при SSH** — проверь, что сервер включён и доступен по 155.212.225.172, порт 22 открыт.
- **Бот не отвечает** — посмотри логи: `ssh root@155.212.225.172 "journalctl -u telegram-bot -n 50 --no-pager"`.
- **Ссылка не выдаётся** — проверь, что `add-ss-user.sh` в `/opt/vpn-bot/` исполняемый и Shadowsocks установлен: `bash /root/setup-shadowsocks.sh`.
