# PROXY protocol на 443 stream (реальные IP клиентов)

## Зачем

nginx stream на `:443` (SNI-роутер, `ssl_preread`) отдаёт наши домены http-nginx на `127.0.0.1:8443`, всё остальное — xray Reality на `127.0.0.1:10443`.
Без PROXY protocol оба видят каждого клиента как `127.0.0.1`: API получает `X-Real-IP: 127.0.0.1`, поэтому вебхуки YooKassa отклоняются (403,
у возвратов нет запасной синхронизации), лимиты по IP становятся общими на всех, IP в аудите бесполезны (на проде 1105 из 1150 запросов за день — `127.0.0.1`).

Режим **выключен по умолчанию** и включается одной командой, которая переключает обе стороны вместе.

## Что меняется в режиме «on»

| Где | off | on |
|---|---|---|
| stream (`/etc/nginx/stream.d/heyearth.conf`, блок с `proxy_pass $backend;`) | домены → `127.0.0.1:8443` | `proxy_protocol on;`, домены → `127.0.0.1:8446` |
| http nginx (`/etc/nginx/conf.d/heyearth.conf`, каждый HTTPS-блок) | `listen 8443 ssl http2;` | то же **плюс** `listen 127.0.0.1:8446 ssl http2 proxy_protocol;`, `set_real_ip_from 127.0.0.1;`, `real_ip_header proxy_protocol;` |
| xray, inbound `vless-reality` (127.0.0.1:10443) | без `sockopt` | `streamSettings.sockopt.acceptProxyProtocol: true` |
| `app_settings.stream_proxy_protocol` | `false` (или нет ключа) | `true` |

- Слушатель `8446` с PROXY есть в http-конфиге всегда (генератор `--nginx`/`--main`/`--site`, и сама команда его дописывает); пока режим выключен, он не используется.
  Обычный `8443` не меняется никогда: прямые локальные проверки без PROXY-заголовка продолжают работать (монитор сертификата в API читает его на `127.0.0.1:8443`).
- Заголовки к API не меняются: `realip` превращает `$remote_addr` в адрес клиента, и существующий `proxy_set_header X-Real-IP $remote_addr;` несёт реальный IP
  (API доверяет `X-Real-IP` только от nginx). Запрос на `8443` без PROXY остаётся `127.0.0.1`.
- `acceptProxyProtocol` получает только `vless-reality`: это единственный inbound за stream. `vless-xhttp` (`:2443`) смотрит в интернет напрямую, CDN-inbound стоит за http-nginx.
- xray с `acceptProxyProtocol` **требует** заголовок: nginx и xray переключаются только вместе. Значение настройки читается строго: только `true`/`false`,
  иначе синхронизация xray прерывается и работающий конфиг остаётся (опечатка не превратится в «выключено» при включённом nginx).

## Как включить (порядок)

1. Задеплоить API с поддержкой настройки (коммит с этим режимом или новее): `go run ./cmd/deploy --update-main` (или `quick_deploy.go`).
   Старый API настройку не знает — команда прождёт 120 с и откатится сама.
2. `go run ./cmd/deploy --stream-proxy-protocol on` и смотреть вывод. Команда:
   - печатает ожидаемый простой: **Reality (Берёза/Дуб)** — новые подключения не проходят ~2–5 с между перезапуском xray и reload nginx, активные Reality-сессии рвутся
     при перезапуске xray; `heyearth-api` перезапускается (~2–5 с); сайт, админка, XHTTP, Бамбук, AWG — без простоя;
   - кладёт скрипт в `/opt/heyearth/ops/stream-proxy-switch.sh` и запускает его на Hub юнитом `heyearth-stream-proxy` (`systemd-run`) — **обрыв SSH его не прерывает**;
     сама команда читает `/var/log/heyearth-stream-proxy.log` и при обрыве переподключается;
   - на Hub: `stream_proxy_protocol = true` → перезапуск `heyearth-api` (стартовая синхронизация переписывает `config.json` и перезапускает xray) →
     ждёт, пока в `config.json` появится `acceptProxyProtocol` и **новый** процесс xray слушает `127.0.0.1:10443` → правит nginx → `nginx -t` → reload → проверка;
   - проверка: `https://hiearth.ru/api/v1/health` через `127.0.0.1:443` → 200; TLS-рукопожатие с Reality-SNI через `127.0.0.1:443` проходит; запрос на публичный
     адрес Hub попадает в `/var/log/nginx/access.log` с этим адресом, а не `127.0.0.1`;
   - при любой ошибке откатывает обе стороны в прежний режим (nginx первым, потом xray) и печатает `RESULT: rolled-back-to-off`;
   - после успеха — внешняя проверка с машины оператора: строка `Внешняя проверка: nginx (и API через X-Real-IP) видит клиента как <ваш IP>`.
3. Убедиться по делу: в журнале аудита админки появляются реальные IP; следующий вебхук YooKassa проходит (`journalctl -u heyearth-api | grep -i yookassa` — без 403);
   Берёза/Дуб/Бамбук реальным Happ.

Выключить: `go run ./cmd/deploy --stream-proxy-protocol off` (обратный порядок: nginx первым, затем настройка и xray; при ошибке — назад в «on»).

## Если что-то пошло не так

- Состояние на Hub: `tail -n 40 /var/log/heyearth-stream-proxy.log` (последняя строка `RESULT: …`), `systemctl status heyearth-stream-proxy` (пока идёт).
  Второй запуск, пока первый не закончился, отказывается.
- Что сейчас включено: `grep -c '"acceptProxyProtocol": true' /opt/heyearth/xray/config.json` (1 — xray ждёт PROXY), `nginx -T | grep -n 'proxy_protocol on'`,
  `runuser -u postgres -- psql -d heyearth -tAc "SELECT value FROM app_settings WHERE key='stream_proxy_protocol'"`. Все три должны говорить одно и то же.
- `RESULT: FAILED` с кодом 2 — не удался и откат: привести вручную в «off»: удалить строку `proxy_protocol on;` в блоке с `proxy_pass $backend;`, в `upstream`
  вернуть `server 127.0.0.1:8443;`, `nginx -t && systemctl reload nginx`, затем `stream_proxy_protocol = false` и `systemctl restart heyearth-api`.
  Копии конфигов nginx до переключения: `/var/backups/heyearth-nginx/*.spp`.
- `--nginx`, `--site`, `--main`, `--setup-xray` генерируют stream по **живому** конфигу xray (есть `acceptProxyProtocol` → stream с PROXY), `--setup-ops` строку
  `proxy_protocol on;` сохраняет — повторный деплой режим не ломает.
