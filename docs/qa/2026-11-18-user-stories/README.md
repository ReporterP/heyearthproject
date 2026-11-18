# User stories QA — 2026-11-18

A walk through HiEarth as its users, one background agent per story. Agents report here; the coordinator re-checks every finding and files the confirmed ones in Beads (epic «User stories QA 2026-11-18»).

The repository is public: no secrets, server addresses, tokens, subscription links or personal data in these files.

## Code under test

On 2026-11-18 the production site and admin served the same bundles as a build of `9441d5c` (branch `security/hardening`; `ace748a`, the admin's commit, has the same `admin_web/`). The API on prod was deployed from `9441d5c` that morning too (deploy worktree at that commit; not re-checked over SSH).

The local stack runs `9441d5c` from a detached worktree `C:/tmp/qa-9441d5c`.

## Local stack

| What | URL | Notes |
|---|---|---|
| API | http://127.0.0.1:8000 | Windows build of `9441d5c`. One local-only change: gRPC listens on `:50151`, since Windows reserves 50000–50059. Postgres runs in docker `hiearth-qa-pg` and is reached through a `docker exec` tunnel on `127.0.0.1:55450`: Docker Desktop's port publishing doesn't reach Windows on this machine. |
| Admin | http://localhost:5173 | vite, `/api` proxied to the API |
| Site | http://localhost:5174 | vite with `vite.qa.config.js` (adds the `/api` proxy) |
| Site mock | http://localhost:5175 | `--mode mock`: control panel `/__mock`, e.g. `/__mock/set?b2b=admin&tenant=suspended`; sign-in token `mock-token` |

- **Test logins** are in `C:/tmp/qa-9441d5c/qa/qa.env`:
  - `ADMIN_USER`/`ADMIN_PASS`: super admin;
  - `qa-support`/`QA_SUPPORT_PASS`: role `qa_support` = `dashboard.read`, `users.read`, `support.manage`.
  Never copy their values into a report.
- **Seed** (`C:/tmp/qa-9441d5c/qa/seed.sh`):
  - plans: `free-trial` (id 1), `vless-reality` (id 4), `all-inclusive` (id 5), `annual-premium` (id 6);
  - promo codes, one per type: `QAPCT20` (−20 %), `QAFIX50` (−50 ₽), `QATRIAL14` (free trial: plan 5, 14 days), `QAGIFT5G` (+5 GB). Also `QAPCTPLAN4` (−30 %, plan 4 only) and `QAEXPIRED` (expired);
  - no servers until story 06 adds them.
- **Database by hand:** `docker exec -i hiearth-qa-pg psql -U qa heyearth`.
- **Not configured locally** (so this is expected, not a bug):
  - YooKassa: creating a payment fails;
  - Telegram, SMTP;
  - Xray and Hysteria binaries: sync is off;
  - the API raises the alert «Нет ни одной exit-ноды».

## Rules for every agent

- **Prod** (`hiearth.ru`, `admin.hiearth.ru`) is read-only:
  - you may open pages and send public GETs;
  - don't create accounts, pay, or press anything that changes data;
  - don't open the admin on prod.
- **Locally** you may create users, subscriptions and rows. Leave the shared settings alone unless your story says otherwise; if you change one, put it back. This covers plans, the B2B flag, the entry mode and site settings. Five other agents use the same database at the same time.
- **The stack is shared:**
  - don't stop or restart servers, Docker or Postgres;
  - don't edit code or config;
  - don't run git commands that change anything.
- **All agents share one address** (127.0.0.1) and its auth limit of 10 requests a minute. A local 429 after a burst is expected: wait a minute and go on, and report it only if it hits a single honest user.
- **Browser:**
  - open your own tab with `tabs_create` and close it at the end;
  - read pages with `get_page_text` / `read_page`;
  - errors: `read_console_messages` (onlyErrors) and `read_network_requests`;
  - take a screenshot only for a visual bug: describe it in the report, and don't save image files in the repo.
- **Security findings** (access to someone else's data, auth bypass, leaked secrets, XSS…) never go into this folder. Write them to the coordinator's scratchpad, outside the repo (`<scratchpad>/qa-security/<story>.md`), and in your report say only «есть находка безопасности, передана координатору».

## Report format (`<NN>-<story>.md`, in Russian)

```markdown
# <NN> <story> — 2026-11-18

Где проверено: локально (9441d5c) / прод (только чтение). Что не удалось проверить и почему.

## Шаги
1. … (URL, what was done)

## Находки
### S<NN>-01 [P1] <short title>
- Где: local http://localhost:5174/… | prod https://hiearth.ru/…
- Шаги: …
- Ожидалось: …
- Получилось: …
- Доказательство: command + the relevant output line / URL + status / console line
- Код (если нашёл): path:line
- Артефакт локального окружения? yes/no/unsure

## Что работает
- one line per checked step that passed
```

Severity:
- **P0**: money or access broken for everyone, or data loss;
- **P1**: the story can't be completed, no workaround;
- **P2**: wrong behaviour with a workaround, or a misleading screen;
- **P3**: copy, cosmetics, a small rule violation.

## Stories

| # | Story | Report |
|---|---|---|
| 01 | Новичок с сайта | [01-newbie.md](01-newbie.md) |
| 02 | Покупатель | [02-buyer.md](02-buyer.md) |
| 03 | Аккаунт | [03-account.md](03-account.md) |
| 04 | Админ | [04-admin.md](04-admin.md) |
| 05 | B2B-клиент | [05-b2b.md](05-b2b.md) |
| 06 | Подписка глазами клиента | [06-subscription.md](06-subscription.md) |

The coordinator's verification of every finding and the ticket map: [triage.md](triage.md).
