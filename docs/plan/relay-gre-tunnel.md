---
id: relay-gre-tunnel
beads_id: heyearthproject-x9n9
prd: ../prd/relay-gre-tunnel.prd.md
status: done
created: 2026-11-20
---

# Plan: GRE-туннель вход ↔ хаб

> Исполнение inline (`superpowers:executing-plans`), ревью одно на фазу (CLAUDE.md, «Экономия токенов»).

**Goal:** все пути вход → хаб (nginx 443/80, DNAT 2443, UDP 30000–65000) идут через GRE хаб ↔ вход. Хэш ECMP один на пару адресов, поэтому потеря «по tuple» исчезает.

**Architecture:** генераторы в `internal/config` (адреса, юниты, nft, nginx, доверие) — единственный источник текста для рецепта, для `cmd/deploy --relay-tunnel` и для тестов. Рецепт получает два шага туннеля. Монитор добавляет :2443 во внешнюю проверку входа.

## Global Constraints

- Пул `10.77.0.0/16`: хаб `base+4·id+1`, вход `base+4·id+2`, `id ∈ [1, 16383]`. MTU 1476, GRE без ключа, `ttl 64`.
- Интерфейсы: хаб `he-r<id>`, вход `he-hub`. Юниты: хаб `heyearth-tunnel-r<id>.service`, вход `heyearth-tunnel.service`.
- Внешние адреса туннеля: хаб `local = RelayOptions.HubIP` (main_server_host), вход `remote = HubIP`, `local = RelayIP`.
- nginx входа: 2 × `хаб_туннель` + 6 × `хаб` `backup`, `proxy_next_upstream_tries 8`, `timeout 10s`, `connect 1s`.
- В документах и тестах только RFC 5737. Комментарии в коде по-английски, тексты владельцу по-русски.

## Review Focus

1. Повтор рецепта на живом туннельном входе — ноль изменений, ноль рестартов: `filesMatch` по юнитам и nft/nginx, проверки «живого» состояния без побочных эффектов. Тест: `TestRelayTunnelFreshThenNoOpRerun`.
2. Пул пересекается с маршрутом провайдера (например, `10.0.0.0/8 via …`) или чужим адресом. Шаг отказывает до любых изменений (`ErrNoRollback`). Тест: `TestTunnelOverlap`.
3. Поменялся адрес хаба: юнит хаба переписывается с новым `local`, юнит входа — с новым `remote`. Оба перезапускаются, а не «уже работает». Тест: в `TestRelayTunnelRestartsOnChange`.
4. `--relay-stream` на туннельном входе не должен откатить туннель. Отказ с подсказкой про `--relay-tunnel`. Тест: `relay_stream_test` новый случай.
5. Монитор: :443 живой (backup), :2443 мёртвый → вход `unhealthy` после трёх проверок. Одна случайная потеря :2443 → повтор и `ok`. Тест: `TestEntryMonitorRelay2443`.

## Контекст из кодовой базы

- `internal/config/relay_nft.go` — nft входа (DNAT на публичный адрес хаба), `RelayUnit`, `RelayHubIP` (только публичный IPv4).
- `internal/config/relay_stream.go` — `RelayNginxConf` (8 копий хаба), `HubRelayTrustFile`, `HubRelayPPConf`.
- `internal/provision/relay.go` — рецепт: preflight → packages → sysctl → rules → unit → stream → firewall → fail2ban → hub_trust → path → activate. Шаблоны шагов: `put` / `unmark` / `undoSh` с pending-маркерами, `check` с `RELAY_FAIL`.
- `internal/provision/relay_stream.go` — `relayHubTrust` / `relayHubTrustCheck` (подменяемые var, хаб — `s.Hub`, `LocalRemote` в API).
- `cmd/deploy/relay_stream.go` — образец отвязанного запуска на хабе, `nodessh`, замер, откат.
- `cmd/deploy/ops_hardening.go` — `ddosAllowScript` (allow4 + доверенный список nginx).
- `cmd/api/entry_monitor.go` — `checkTCP` (TLS :443 через выход), `verifyRelay` (443 + 2443 + Бамбук).
- `cmd/api/bamboo_probe.go` — salamander + VN-проба (повторить на Python для матрицы).

## Затрагиваемые файлы

### Создать
- `server/backend/internal/config/relay_tunnel.go` (+ `_test.go`, golden `relay_tunnel_unit.golden`, `hub_tunnel_unit.golden`)
- `server/backend/internal/provision/relay_tunnel.go` (+ `_test.go`)
- `server/backend/cmd/deploy/relay_tunnel.go` (+ `_test.go`), `cmd/deploy/relay_matrix.py` (embed)
- `docs/runbooks/relay-tunnel.md`, `docs/tasklist/relay-gre-tunnel.md`

### Изменить
- `internal/config/relay_nft.go` — `RelayNftRuleset(hubIP, hubTun string)`, MSS clamp; `relay_stream.go` — `RelayNginxConf(hubIP, hubTun string)`, `HubRelayTrustFile(relayIP, relayTun string)`; golden.
- `internal/provision/relay.go` — `RelayOptions.ServerID`, шаги, порядок, preflight (повторы портов, проверка туннеля), firewall, teardown; `relay_stream.go` — доверие двум адресам.
- `cmd/api/provision_wiring.go` — `o.ServerID = n.ID`.
- `cmd/api/entry_monitor.go` — :2443 во внешней проверке входа.
- `cmd/deploy/ops_hardening.go` — peers туннелей в `ddosAllowScript`; `cmd/deploy/main.go` — флаги; `cmd/deploy/relay_stream.go` — отказ на туннельном входе.
- `docs/incidents/2026-11-20.md`, `CLAUDE.md` (Entries + gotcha), `docs/tasklist/INDEX.md`.

## Декомпозиция

### Task 1: генераторы туннеля (`internal/config`)
**Produces:**
- `RelayTunnelAddrs(id int) (hub, relay netip.Addr, err error)`;
- `HubTunnelDev(id int) string`;
- `HubTunnelUnitName(id int) string`;
- `const RelayTunnelDev = "he-hub"`, `RelayTunnelUnitName = "heyearth-tunnel.service"`, `RelayTunnelMTU = 1476`, `RelayTunnelPool = "10.77.0.0/16"`;
- `TunnelUnit(dev, local, remote string, addr, peer netip.Addr) (string, error)`;
- `TunnelOverlap(routes, addrs string, ours []netip.Addr, dev string) error` — разбор `ip -4 -o route show table all` и `ip -4 -o addr show`;
- `RelayNftRuleset(hubIP, hubTun string)` («» = без туннеля, как сейчас);
- `RelayNginxConf(hubIP, hubTun string)`;
- `HubRelayTrustFile(relayIP, relayTun string)`.

**Tests:**
- `TestRelayTunnelAddrs`: id 6 → `10.77.0.25`/`10.77.0.26`; id 0 и 16384 → ошибка; id 16383 → `10.77.255.253`/`.254`.
- `TestTunnelOverlap`: `10.0.0.0/8 via` → ошибка; `default` и маршруты своего `dev` — ок; адрес `10.77.0.25/32` на `eth1` → ошибка.
- `TestRelayNftTunnelGolden`: DNAT на туннель, clamp `oifname "he-hub"`, masquerade.
- `TestRelayNginxTunnelGolden`: 2 primary + 6 `backup`; `nginx -t` в docker (как у `816295c`), если есть.
- Golden юнитов.

**Verify:** `go test ./internal/config/`.

### Task 2: шаги рецепта (`internal/provision`)
**Consumes:** Task 1.

**Produces:**
- шаг `relay_hub_tunnel` (хаб, через `s.Hub`): проверка пересечения → снимок маршрутов → предохранитель (только для нового юнита) → юнит + `ufw allow proto gre … comment heyearth-tunnel-<id>` → start/restart → allow-скрипт → снимок «после» равен «до» → снять предохранитель; Verify: юнит, `ip -d link show he-r<id>` (local/remote/адреса), правило ufw, адрес peer в `allow4` (если таблица есть); Rollback: вернуть прежний юнит или снять новый, удалить правило ufw нового;
- шаг `relay_tunnel` (вход): `put` юнита с маркером → start/restart → `ping -c 3 -W 2 хаб_туннель` хотя бы один ответ; Verify: юнит, link, ping; Rollback: `undoSh` (прежний или снять);
- `relay_hub_trust` сразу после туннеля, доверие двум адресам;
- preflight: проверка «tunnel» (пул, `ip_gre`), порты хаба с 3 попытками;
- firewall: `ufw allow proto gre from $HUB`, route на `хаб_туннель`;
- teardown: юнит туннеля входа; deprovision: шаг `relay_hub_tunnel_remove`.

**Tests:**
- `TestRelayTunnelFreshThenNoOpRerun`;
- `TestRelayTunnelRollback` (ping не проходит → юнит снят, nft не тронут);
- `TestRelayTunnelRestartsOnChange`;
- `TestHubTunnelRefusesOverlap`;
- `TestHubTunnelRollsBackOnRouteChange`;
- `TestRelayDeprovision` дополнить;
- `TestRelayStitching` обновить порядок.

**Verify:** `go test ./internal/provision/`.

### Task 3: монитор и wiring (`cmd/api`)
- `checkTCP`: для `relay` после успешного :443 — `tlsCheck(port, addr, 2443, sni)`, при ошибке ещё одна попытка. Ошибка с префиксом `TLS :2443`.
- Текст алерта: `systemctl status heyearth-tunnel; ip -d link show he-hub`.
- `apiRelayOptions`: `o.ServerID = n.ID`.
- **Tests:** `TestEntryMonitorRelay2443` (3 случая из Review Focus 5).
- **Verify:** `go test ./cmd/api/ -run 'Entry|Relay'`.

### Task 4: хаб — allow-скрипт (`cmd/deploy/ops_hardening.go`)
- `ddosAllowScript`: `peers=$(ip -o -4 addr show | awk '$5 == "peer" { sub(/\/.*/, "", $6); print $6 }')` идут в те же `allow4` и доверенный список.
- **Test:** `TestDDoSAllowScriptTunnelPeers` (stub `ip`, `nft`, `psql` → `allow4` содержит `10.77.0.26`).

### Task 5: `deploy --relay-tunnel ID [--relay-tunnel-check]` (`cmd/deploy/relay_tunnel.go`)
- БД на хабе: `role='relay'`, `ip_address`, `main_server_host` (IP), `reality_sni_domain`, `hysteria2_obfs_password`, live-выход (`role='exit' AND lifecycle_state='online'`, наименьший id).
- Скрипт на хабе (отвязанный, `flock`, лог):
  1. замер «до» (выход + вход);
  2. хаб: проверка пересечения, предохранитель 20 мин, юнит, ufw, allow-скрипт, доверие + `nginx -t` + reload, сравнение маршрутов;
  3. вход (`nodessh`, 15 попыток): предохранитель 15 мин, юнит туннеля, ping, nft (`systemctl reload heyearth-relay`), nginx (`nginx -t` + reload);
  4. замер «после»;
  5. решение: OK → подтвердить вход, затем хаб (`enable` юнитов только здесь); иначе → откат входа, затем хаба.
- Матрица (`relay_matrix.py`, на выходе через `nodessh`): `tcp=<ok>/<n> udp=<ok>/<n>`, 100 + 100, source port 42001…, hop-порты 30000 + k·350.
- **Tests:** рендер скрипта (`bash -n`), разбор `MATRIX`, решение «до/после» (`relayTunnelVerdict`), Python-матрица против локального UDP/TLS-сервера (если есть `python3`).

### Task 6: документы и выдача
- Runbook `docs/runbooks/relay-tunnel.md`, CLAUDE.md («Entries», gotcha), инцидент (итог после замеров), tasklist, INDEX.
- Ревью диффа (одно, `code-reviewer`, sonnet), правки.
- Слияние в `main` (ff), затем деплой по «да» владельца: API (`--update-main` из чистого worktree на `main`), затем `--relay-tunnel 6`.

## Test strategy

- [ ] `go test ./internal/config/ ./internal/provision/ ./cmd/deploy/ ./cmd/api/` — зелёные.
- [ ] `go vet ./...`, `go build ./...`.
- [ ] Docker-лаборатория (если ядро Docker Desktop умеет `ip_gre`): GRE + nft входа + потеря по hash(sport) на транзите → через туннель 100 %.
- [ ] Прод: `--relay-tunnel-check 6` (до), `--relay-tunnel 6`, матрица после ≥ 99 %, `deploy --status`, `journalctl -u heyearth-api -n 20`.

## Rollback plan

- Автоматически: `--relay-tunnel` откатывает оба конца при «после < 99 % или хуже, чем до». Предохранители снимают изменения сами, если запуск оборвался.
- Вручную, вход: `systemctl disable --now heyearth-tunnel`, вернуть `/etc/heyearth/relay.nft.heyearth-prev` и `/etc/nginx/nginx.conf.heyearth-prev`, `systemctl reload heyearth-relay nginx`.
- Вручную, хаб: `systemctl disable --now heyearth-tunnel-r6`, `ufw delete` правила `heyearth-tunnel-6`, убрать строку туннеля из `/etc/nginx/heyearth-relays/<ip>.conf`, `nginx -t && systemctl reload nginx`.
- API: прежний бинарник (`heyearth-api.bak`).

## Deployment notes

- Новых env и миграций нет.
- Порядок:
  1. API (`--update-main`): рецепт и монитор;
  2. `--relay-tunnel-check 6`;
  3. `--relay-tunnel 6`.
- Рестартов xray/hysteria нет. nginx хаба — один reload (доверие). На входе: reload nginx и `heyearth-relay`.
