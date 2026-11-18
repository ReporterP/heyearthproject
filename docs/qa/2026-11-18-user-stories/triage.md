# Triage of the 2026-11-18 user-stories QA

The coordinator re-checked every agent finding on 2026-11-18 against `9441d5c` (the prod site/admin bundles match a build of it).

- **Tickets:** Beads epic `heyearthproject-jzd5` «User stories QA 2026-11-18», children `.1`–`.31`.
- **Security:** findings are not listed here. They are four children with the label `security`.
- **Severity:** the coordinator's, where it differs from the agent's (noted).

How each finding was verified:
- **live:** reproduced in the browser or with curl on the local stack;
- **code:** confirmed by reading the code at `9441d5c`;
- **prod GET:** a public read on prod.

| Finding | Sev | Verdict | How | Ticket |
|---|---|---|---|---|
| S01-01 referral card overflows on a phone | P2 | confirmed | live: scrollWidth 451 at 375 | jzd5.1 |
| S01-02 country / mobile block hardcoded | P3 (agent P2) | confirmed; correct on prod today | code | jzd5.5 |
| S01-03 eco names and «протоколы» in plan data | P2 | confirmed | prod GET `/api/v1/plans` | jzd5.9 |
| S01-04 legal pages render placeholders | P2 | confirmed | code | jzd5.2 |
| S01-05 refund rules contradict | P2 | partly: «by e-mail» vs «from the cabinet» confirmed; «balance not refunded» vs «refund the payment» may be two cases | code | jzd5.10 |
| S01-06 «безлимитный» hides the 10 GB trial | P3 | confirmed | code + prod GET | jzd5.6 |
| S01-07 static «Всё работает» | P3 | confirmed | code | jzd5.14 |
| S01-08 «Открываем Happ…» stays when not installed | P3 | confirmed | code | jzd5.6 |
| S01-09a two «Копировать» on the ID screen | — | not a defect: spec 07, row 6 | spec | — |
| S01-09b,c Linux in apps; label twice | P3 | confirmed | code | jzd5.6 |
| S01-09d,e «прокси-маршрутизации»; privacy without Telegram | P3 | confirmed | code | jzd5.10 |
| S02-01 code limiter blocks the price quote | P2 | confirmed: fixed 10-min window, constant `Retry-After: 60` | code | jzd5.3 |
| S02-02 traffic gift spent on unlimited plans | P2 | confirmed | code | jzd5.4 |
| S02-03 auto-renew a day early vs «в день окончания» | P3 | confirmed; renewal itself works (live: charged at the hourly tick ~2 h before the end, +30 days) | live + code | jzd5.12 |
| S02-04 top-up error reason hidden | P3 | confirmed | code | jzd5.7 |
| S02-05 free-trial hint doesn't name the plan | P3 | confirmed | code | jzd5.6 |
| S02-06 amount lost after resume | P3 | confirmed | code | jzd5.7 |
| S02-07 /payments empty after a wallet purchase | P3 | confirmed | code | jzd5.6 |
| S02-08 admin reason shown to the client | P3 | confirmed | code | jzd5.13 |
| S02-09 eco plan names | — | duplicate of S01-03 | — | jzd5.9 |
| S02-10 receipt without e-mail may fail at YooKassa | — | not verifiable locally: needs a real payment | — | owner check |
| S03-01 no devices / sessions screen | P2 | confirmed | code | jzd5.11 |
| S03-02 minimum password length 4 | P3 | confirmed; already an owner item (`el3d.12`) | code | — |
| S03-03 two «Копировать» | — | not a defect: spec 07, row 6 | spec | — |
| S03-04 «восстановить нельзя» vs «найдём по платежу» | P3 | confirmed | code | jzd5.6 |
| S03-05 logout lands on `/login?redirect=` | P3 | confirmed | live | jzd5.8 |
| S03-06 no-token 401 says «Сессия истекла» | P3 | confirmed | curl | jzd5.8 |
| S04-01 request storm on /admins | P2 (agent P1) | confirmed on /admins (105 requests in ~2 s); not reproduced on /tenants | live | jzd5.15 |
| S04-02 empty states instead of a refusal | P2 | confirmed | live | jzd5.16 |
| S04-03 double error toasts | — | not reproduced for prod: React StrictMode doubles effects in dev only | code | — |
| S04-04 «Создать OK» toasts | P3 | confirmed | code | jzd5.17 |
| S04-05 hub-key 500 + raw error | P3 | 500 = local artefact; raw `err.Error()` in the UI confirmed | code | jzd5.17 |
| S04-06 admins / roles can't be deleted | P3 | confirmed | code | jzd5.18 |
| S04-07 labels, «Del», expired promo «активен» | P3 | confirmed | code | jzd5.17 |
| S04-08 page count in docs | — | not a defect: 18 pages + login = the 19 in CLAUDE.md; «23» was wrong | code | — |
| S05-01 suspended company never resumes | P1, dormant | confirmed | code | jzd5.23 |
| S05-02 role «Сотрудник» gets an error screen | P1, dormant | confirmed | code | jzd5.24 |
| S05-03 seat limit not enforced | P2, dormant | confirmed | code | jzd5.25 |
| S05-04 invoice number mismatch | P3 (agent P2), dormant | confirmed | code | jzd5.26 |
| S05-05 suspended blocks nothing | P1, dormant | confirmed | code | jzd5.23 |
| S05-06 landing copy vs product | P2, dormant | confirmed | code | jzd5.27 |
| S05-07 cabinet details, mock vs API | P3, dormant | confirmed | code | jzd5.26 |
| S06-01 nothing bumps `config_version` | P2 | confirmed | code (grep: no writer) | jzd5.19 |
| S06-02 banner clears only on «Понятно» | — | by design (server can't know the fetch) | code | noted in jzd5.19 |
| S06-03 Clash Express SNI = `main_server_host` | P3 | code confirmed; prod value unknown | code | jzd5.21 (needs-info) |
| S06-04 Clash brand «HeYearth» | P3 | confirmed | code | jzd5.20 |
| S06-05 duplicate lines in unpinned failover | P3 | confirmed; pinned by goldens | code | jzd5.22 |

«Dormant» means `b2b_enabled` is off on prod; these must be settled before B2B is switched on.

## Stand notes (not product bugs)

- **Docker Desktop on this machine** doesn't publish container ports to Windows. WSL runs with `networkingMode=mirrored`, and Windows↔WSL loopback fails in both directions. Postgres was reached through a `docker exec … nc` tunnel.
- **Windows reserves TCP 50000–50059.** The API's hardcoded gRPC `:50051` can't bind, so the local build moved it to `:50151`.
- **Browser tabs share `localStorage` per origin.** Agents overwrote each other's tokens on `localhost:5174` until each moved to its own `qaNN.localhost` origin.
- **One agent typed** the local test admin login into another agent's site tab (local, test values only).
- **The shared 127.0.0.1 address** made the auth and code limiters trip often. This inflated S02-01 but doesn't explain it away.

## Fixes (2026-11-18, the owner chose «security + P2»)

Branch `claude/affectionate-khayyam-242881`, rebased on `security/hardening` `184d36c`. It is not merged and not deployed; the merge and deploy are tracked in `jzd5.32`.

| Ticket | Commit | Verified |
|---|---|---|
| jzd5.1 referral card on a phone | `8557944` | live at 375: no horizontal scroll |
| jzd5.2 legal pages wait for the details | `5e8e435` | live: error + «Повторить», no placeholders |
| jzd5.3 quote limiter, truthful Retry-After | `03f0385`, `a630b2e` | Go tests; live: 15 plain quotes 200, the 11th coded one 429 with `Retry-After: 599` |
| jzd5.4 traffic gift on unlimited plans | `a630b2e` (+ `03f0385`) | PG test; live: 400 with the cabinet's text |
| jzd5.15 admin request storm | `e4aab41` | live: 2 requests instead of 105 |
| jzd5.16 «Нет доступа» instead of empty pages | `e4aab41`, `d08f13f` | node tests; live as `qa-support` |
| jzd5.19 «Обновите подписку» banner | `dfeea4e` | live: a version bump shows it, «Понятно» hides it |
| jzd5.25, .28–.31 (security, B2B seats) | `800d45c`, `82d4992`, `e1e9c7b` | PG tests; .28/.29 also live |

A code review of the fixes found three more problems, fixed in `e1e9c7b`:
- password-change attempts are now reserved atomically;
- blocked login names can't be enumerated;
- the claim-code row is locked.

Final checks on `e1e9c7b`:
- `go vet` is clean;
- `go test ./cmd/... ./internal/...` with Postgres tests: 17 packages ok;
- `admin_web` `npm test`: 176/176;
- site and admin builds pass;
- `impeccable detect` was not run: it isn't installed.
