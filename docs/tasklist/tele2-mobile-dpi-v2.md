---
id: tele2-mobile-dpi-v2
beads_id: heyearthproject-ei6j
plan: ../plan/tele2-mobile-dpi-v2.md
status: ready
created: 2026-05-27
---

# Tasklist: Tele2 mobile DPI mitigation v2 (chrome fp + Yandex Cloud SNI)

Зеркало Beads для человекочитаемой навигации. Single source of truth — `bd list --tree heyearthproject-ei6j`.

## Фаза 1: Code change (binary deploy)

- [ ] **heyearthproject-9mqz** — T1: Switch Reality/XHTTP fp from ios+safari to chrome+firefox
  - Файлы: `server/backend/cmd/api/client_api.go:218-265`
  - Verify: `go build ./cmd/api/...` exit 0; `grep -nE '"ios"\|"safari"' cmd/api/client_api.go` ничего не находит
  - Commit prefix: `feat(reality): switch URI fp from ios+safari to chrome+firefox`

## Фаза 2: Production state change

- [ ] **heyearthproject-nuyf** — T2: Hard-switch reality_sni_domain + restart heyearth-api (blocks: 9mqz)
  - Файлы: prod PostgreSQL `app_settings.reality_sni_domain`, prod service `heyearth-api`
  - Verify: `psql -c "SELECT value FROM app_settings WHERE key='reality_sni_domain'"` = `storage.yandexcloud.net`; `systemctl is-active heyearth-api` = `active`
  - Не code-commit — production state change

## Фаза 3: Verification

- [ ] **heyearthproject-ywbk** — T3: Verify chain DB → xray config → subscription URI (blocks: nuyf)
  - Verify a: `jq '.inbounds[] | select(.protocol=="vless") | .streamSettings.realitySettings.dest' /opt/heyearth/xray/config.json` = `storage.yandexcloud.net:443`
  - Verify b: `systemctl is-active xray.service` = `active`; нет error в journalctl --since '2 min ago'
  - Verify c: `curl -H 'User-Agent: Happ' /api/v1/user/subscribe/d329def4-...` возвращает 4 vless:// с `sni=storage.yandexcloud.net` и чередующимися `fp=chrome`/`fp=firefox`
  - Verify d: `openssl s_client -connect storage.yandexcloud.net:443 -servername storage.yandexcloud.net` показывает валидный Yandex CA cert

- [ ] **heyearthproject-u54t** — T4: E2E test on Tele2 mobile (blocks: ywbk)
  - Interactive с другом пользователя на Tele2 mobile + Happ
  - Verify: `journalctl -u xray.service --since '<T_START>' | grep '5f5f66b7-'` показывает 10+ accepted-записей на реальные destinations (telegram/google) за окно теста
  - Verify: friend report — ping стабильно OK 3/3, google.com открылся, t.me открылся
  - На успехе — `bd remember` insight для future sessions

## Side quests / Conditional

- [ ] **heyearthproject-harz** — T5: ROLLBACK (conditional, P0)
  - Триггер: T3 нашёл xray crash/wrong URI ИЛИ T4 friend report'нул "no improvement"
  - Action: `UPDATE app_settings SET value='yandex.ru'` + `systemctl restart heyearth-api`
  - Verify: `jq dest` снова `yandex.ru:443`
  - Решение про fp-фикс: оставить если partial improvement, revert если регресс

## Discovered side quests
_(пусто — заполнится во время execute если найдём что-то связанное но out-of-scope)_

## Deferred
- **B-фикс** (Hysteria2 primary для UA=Happ) — обсуждается после оценки результата A+C
- Перенос XHTTP-профиля Дуб с :2443 на :443 через nginx stream — если A+C недостаточно
- Wire `reality_fingerprint` setting из app_settings в client_api.go — технический долг (auto-tuner пишет, никто не читает)

## Verification log
_(заполнится по мере закрытия каждой задачи)_
