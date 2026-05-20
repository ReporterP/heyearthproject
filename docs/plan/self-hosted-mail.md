---
id: self-hosted-mail
beads_id: heyearthproject-baga
prd: (none — operational task)
status: in_progress
created: 2026-05-20
---

# Plan: Self-hosted mail server (Mailcow) on dedicated VPS

## Контекст

Текущее состояние почты в HiEarth (verified live на prod 2026-05-20):
```
smtp_enabled    = false
smtp_host       = (empty)
smtp_user       = (empty)
smtp_password   = (empty)
```

Т.е. **никаких писем сейчас не отправляется**. P0/P1/P2 B2B-флоу
(welcome, claim-link, tenant credit, suspension, low-balance) — все вызовы
`email.Send(...)` сейчас silent no-op. До починки B2B-signup технически
работает, но юзер не получает claim-ссылку и не может привязаться к тенанту.

Hub-сервер (45.150.39.236, vdsina): 1 vCPU, 1.9 GB RAM, load avg ≈ 1.10,
swap = 0. Не подходит для co-hosting mail:
* RAM: Mailcow требует ≥ 4 GB; на hub останется −300 MB до OOM.
* Port 25 outbound заблокирован VDS-провайдером (стандарт у vdsina).
* IP 45.150.39.236 проксирует VPN-трафик 100+ юзеров — почти гарантированно
  в Spamhaus/Barracuda/Cisco Talos proxy-listings.

## Архитектурное решение

Отдельный VPS под **Mailcow** (Docker-композ: Postfix + Dovecot + Rspamd +
SOGo webmail + UI админка). Один сервер делает и SMTP-relay для
hiearth-api (исходящие), и IMAP/webmail для команды (входящие, личные
ящики).

### Рассмотренные альтернативы

1. ✅ **Mailcow на VPS** — выбрано. Один стек, годами в продакшене у
   тысяч компаний, минимум ручной работы. DKIM/SPF/DMARC из коробки.
2. ❌ Отдельные Postfix + Dovecot + Rspamd собирать руками. Точечнее, но
   ~неделя на отладку и + долгое поддержание.
3. ❌ Mail-in-a-Box. Похож на Mailcow, но требует владения всем сервером
   (заточен под small-business, плохо ужимается).
4. ❌ Hub-co-host. Не работает по 3 причинам выше.

## Затрагиваемые файлы

### Создать
- `server/backend/cmd/deploy/mail-setup.sh` ✅ написан — однократно запускается
  на новом VPS, ставит Docker + Mailcow + UFW + базовые правила.
- `docs/plan/self-hosted-mail.md` — этот документ.

### Изменить
- Записи DNS в реестре `hiearth.ru` (см. таблицу ниже).
- `app_settings` в HiEarth-БД через админ-панель — после того как почта
  заработает, заполнить smtp_* поля.

### Удалить
- (ничего)

## Декомпозиция

### Phase 1 — Provisioning (зависит от пользователя)
1. **Купить VPS** (~15 мин)
   - Aeza VPS-2 Moscow / Timeweb VDS-1 / Selectel Cloud / Hetzner CX22
   - Минимум: 2 vCPU, 4 GB RAM, 40 GB SSD, Ubuntu 24.04 LTS
   - Цена: 250-590 ₽/мес
2. **Запросить PTR-запись** у провайдера: `<IP> → mail.hiearth.ru`
   - У Aeza / Timeweb / Selectel — через ЛК
   - У Hetzner — через "Reverse DNS" в Cloud Console
3. **Дать SSH-доступ** (root + IP) или развернуть свой ключ.

### Phase 2 — DNS pre-staging (можно параллельно с Phase 1)
4. **Создать A-запись** `mail.hiearth.ru → <IP VPS>` сразу после провижна.
   Это нужно до запуска Mailcow — Let's Encrypt будет валидировать домен
   через HTTP-challenge.

### Phase 3 — Установка Mailcow (~30 мин с моей стороны)
5. **Запустить setup-скрипт** на VPS:
   ```bash
   ssh root@<IP>
   cd /tmp
   wget https://raw.githubusercontent.com/.../mail-setup.sh   # или scp с локали
   bash mail-setup.sh mail.hiearth.ru
   ```
   Скрипт делает: apt update → docker engine → ufw rules → git clone Mailcow
   → generate_config.sh → docker compose up. Минут 10-15, в основном на
   pull Docker-образов.

6. **Открыть админку Mailcow** на `https://mail.hiearth.ru/`
   - Дефолтные креды: `admin / moohoo`
   - Поменять пароль сразу (Configuration → Administrators)

7. **Добавить домен** `hiearth.ru` (Mail Setup → Domains).

8. **Создать ящики**:
   - `noreply@hiearth.ru` — для transactional (используется hiearth-api)
   - `support@hiearth.ru` — для общения с клиентами
   - `alex@hiearth.ru` — личный
   - `dmarc@hiearth.ru` — приёмник DMARC reports

9. **Сгенерировать DKIM-ключ** в Mailcow (ARC/DKIM keys → + Add key,
   selector=`dkim`, размер 2048). Скопировать значение публичного ключа.

### Phase 4 — Добивка DNS
10. **Залить DNS-записи** в реестр (см. таблицу ниже).

11. **Подождать** propagation (5-30 мин) и проверить:
    ```bash
    dig +short mail.hiearth.ru A
    dig +short hiearth.ru MX
    dig +short TXT hiearth.ru | grep spf
    dig +short TXT dkim._domainkey.hiearth.ru
    dig +short TXT _dmarc.hiearth.ru
    ```

### Phase 5 — Проверка качества (~10 мин)
12. **mail-tester.com**: отправить тестовое письмо с
    `noreply@hiearth.ru` (через webmail на `mail.hiearth.ru`) на адрес
    выданный mail-tester'ом. Цель: ≥ 9/10.

13. **Если оценка < 9**:
    - Проверить PTR (`dig -x <IP>` должен вернуть `mail.hiearth.ru.`).
    - Проверить SPF / DKIM / DMARC — типичные ошибки: лишний пробел в
      TXT-записи, забыли точку в конце MX.
    - mail-tester сам подскажет что не так.

### Phase 6 — Интеграция с HiEarth
14. **Заполнить SMTP** в админке `admin.hiearth.ru/settings`:
    ```
    smtp_host        = mail.hiearth.ru
    smtp_port        = 587
    smtp_use_tls     = true
    smtp_user        = noreply@hiearth.ru
    smtp_password    = <пароль ящика noreply>
    smtp_from_email  = noreply@hiearth.ru
    smtp_from_name   = HiEarth
    smtp_enabled     = true
    ```

15. **End-to-end тест**: создать тестовую регистрацию через
    `/business/signup` → проверить что welcome-email пришёл на указанный
    email; и что claim-link в нём работает.

## Test strategy

Evidence-driven (нет автотестов):

```bash
# 1. Mailcow здоров
ssh root@<mail-vps> 'cd /opt/mailcow-dockerized && docker compose ps'
# Все контейнеры status="Up", healthy.

# 2. Порты слушают
ssh root@<mail-vps> 'ss -tln | grep -E ":(25|465|587|143|993|443)\b"'

# 3. DNS отдают правильные значения
for r in A MX TXT; do echo "=== $r ==="; dig +short $r hiearth.ru; done
dig +short TXT dkim._domainkey.hiearth.ru
dig +short TXT _dmarc.hiearth.ru
dig +short -x <mail-vps-ip>  # должно быть mail.hiearth.ru.

# 4. SMTP принимает submission
echo "QUIT" | openssl s_client -starttls smtp -connect mail.hiearth.ru:587 -crlf 2>&1 | head -20
# Должен пройти TLS-handshake.

# 5. mail-tester.com даёт ≥ 9/10.

# 6. HiEarth fires emails: trigger /business/signup, check email_log
sudo -u postgres psql heyearth -c "SELECT id, recipient, subject, sent_at, error FROM email_log ORDER BY id DESC LIMIT 10;"
```

## Rollback plan

Если что-то сломалось:
1. На VPS: `cd /opt/mailcow-dockerized && docker compose down` —
   останавливает почту. HiEarth-api при `smtp_enabled=false` снова
   silent-skip-ит письма (как до этой работы — поломок не будет).
2. В админке HiEarth: `smtp_enabled=false`. Без этого hiearth-api
   будет 30 сек висеть на каждом email-вызове ожидая SMTP.
3. DNS-записи можно оставить или удалить — на VPN они не влияют.

## Deployment notes

- Mailcow обновляется через `update.sh` в корне репо. Раз в месяц.
- Backup: контейнер `mailcow-helper` умеет `helper-tools/mailcow-backup`.
  В первый раз настроить cron на S3-backup ящиков.
- Disk: 40 GB хватит примерно на полгода для 5-10 активных ящиков.
  Когда `df -h /var/lib/docker/volumes/mailcowdockerized_vmail-vol-1` >
  70%, надо расширять.
- IP warmup: первые 1-2 недели не слать > 50 писем/день — иначе Gmail
  flag-нет как "новый отправитель → подозрительно". Постепенно
  наращивать.

## Готовые DNS-записи (заполнить после установки)

| Тип | Имя | Значение | TTL |
|---|---|---|---|
| A | `mail` | `<VPS IP>` | 300 |
| MX | `@` (или `hiearth.ru`) | `10 mail.hiearth.ru.` | 300 |
| TXT | `@` | `"v=spf1 mx -all"` | 300 |
| TXT | `dkim._domainkey` | (значение из Mailcow → ARC/DKIM keys) | 300 |
| TXT | `_dmarc` | `"v=DMARC1; p=quarantine; rua=mailto:dmarc@hiearth.ru"` | 300 |
| CNAME (опц.) | `autoconfig` | `mail.hiearth.ru.` | 300 |
| PTR | `<VPS IP>` | `mail.hiearth.ru` | (выставляет провайдер VPS) |
