# Changing the Hub's public address from the admin — design

Date: 2026-11-20 · Owner decisions in chat on 20.11 · Beads: (epic created with the plan)

Addresses below are RFC 5737 examples: `192.0.2.10` = the address the Hub uses today
(`main_server_host`, route source), `198.51.100.20` = the new one, `203.0.113.5` = the old primary
that carries the gateway. Real addresses live in `server/.env` and the admin, never here.

## Intent

The owner, 20.11: «на мобильной сети опять не работает IP хаба. В прошлый раз мы меняли DNS-запись, и
ты потом проходился по всем подпискам и заменял им IP. Сделай в админке историю, чтобы я мог заменить
IP у хаба и всё нормально работало у пользователей».

The goal is a procedure the owner runs alone from the admin: add a second address to the same VPS,
prove it works (from abroad, from RU, from a phone on a mobile network), move everything that points at
the Hub onto it, keep the old one alive for a transition, retire it. Users keep working throughout; the
only planned interruption is Бамбук for the minutes the entries take to re-point.

## How it was done last time (session «MyBigProject», 2026-10-10..12)

By hand, over SSH:
1. `ip addr add <new>/32 dev ens3` (VNC), then the address persisted in `/etc/netplan/01-netcfg.yaml`
   (backup next to it), `netplan generate` only.
2. `UPDATE app_settings SET value=<new> WHERE key='main_server_host'`, `config_version + 1`; xray-sync
   picked it up.
3. The owner changed the A record in Cloudflare.
4. `DEPLOY_HOST` in `server/.env`.
5. 12.10: the default route's source was switched to the new address (netplan `from:`); Бамбук died for
   7 h because port hopping used `REDIRECT` (fixed with port-only DNAT; `feedback_hub_nat_source_ip`).

Since then the address is baked into more places, so those five steps would now break users.

## Where the Hub's address lives today

| Place | What it holds | Effect of a change |
|---|---|---|
| `app_settings.main_server_host` (IP) | where users' Hub lines connect: «· напрямую», Бамбук `hub` way, «Авто» fallback without entries; entry 0 of the monitor | lines follow at the next subscription refresh |
| XHTTP Host (`xray_sync.go` inbound `host`, `subscription_json.go`, `client_api.xhttpURI`, clash `xhttp-opts.host`) | `main_server_host` | the inbound checks it: changing the setting breaks Дуб for everyone, through entries too, until each client refreshes |
| Entries (`servers.role='relay'`) | nft DNAT TCP 2443 + UDP 30000–65000 and nginx stream upstream `:8447`/`:80` → `main_server_host`, baked by `relay_rules` / `relay_stream`; ufw SSH allow from Hub IPs (`relay_firewall`) | all traffic through entries (every mobile user in mode `relay`) keeps going to the old address until each entry is reprovisioned |
| Exits | firewall (`firewall` step: SSH + TCP 50052 from Hub IPs), agent guard (`node_agent`: 50052 from Hub IPs) | if the Hub's outbound source changes first, the API loses node-agent and SSH to exits; tunnels (UDP 50443, TCP 443) are open to all |
| Hub route source (netplan `from:`, manual) | source of everything the Hub originates | hysteria (listens `0.0.0.0`, Salamander, no `IP_PKTINFO`) answers from the route source: Бамбук works on exactly one address at a time |
| `hubIPCache` (`hub_addrs.go`) | Hub addresses for the xray/hysteria «refuse the Hub's own addresses» rules, exit/relay allow lists, tgweb | cached for the process lifetime; a new address needs a reset |
| DNS (Cloudflare, owner's panel) | `hiearth.ru`, `admin` → Hub; `go`, `tg` → an entry | site, admin, certbot HTTP-01, links on `hiearth.ru` |
| Hub listeners | nginx `listen 443/80`, xray XHTTP `0.0.0.0:2443`, hysteria `0.0.0.0`, DNAT `--dst-type LOCAL`, egress guard `--dst-type LOCAL`, `heyearth_ddos` (source-based) | none: they serve any local address |
| Owner's PC | `server/.env DEPLOY_HOST`, scratchpad forwarders | a hint at the end; the host key does not change |

Not affected: Reality keys/SNI, the Бамбук certificate (domain `hub_main_server_host`), exits' and entries'
own addresses, `heyearth_ddos` allow sets, PROXY trust per entry, YooKassa (webhooks by domain).

## Goals

- One admin procedure «Хаб → Адрес» from «I have a new IP from VDSina» to «old address retired».
- Every step idempotent, re-runnable, logged, audited; a failed step says what failed and how to retry.
- The address is proved before anything points at it: outbound from it to every entry/exit and the
  external services the Hub uses; inbound to it from abroad, from RU, from the owner's phone on a mobile
  network.
- The one dangerous change (route source) arms a revert timer before it is applied and keeps it armed
  until the owner confirms from the admin; no confirmation → everything goes back by itself.
- During the transition subscriptions refresh hourly and the wizard shows how many active users already
  fetched the new address and how much traffic still arrives at the old one.
- Дуб's Host header no longer depends on the address (prerequisite, shipped first).

## Non-goals

- Moving the Hub to another VPS (database, keys, CA): a different, much larger feature.
- Changing DNS through the Cloudflare API (owner, 20.11: DNS by hand; the wizard tells what to change
  and waits until it resolves). Can be added later behind the same «DNS converged» gate.
- Two hysteria processes on two addresses to remove Бамбук's window (owner accepted the window).
- IPv6, exits' or entries' own address changes.
- A «🌳 Напрямую only WiFi» line in the JSON subscription (discussed 20.11; separate task, the incident
  session is reviewing the subscription).

## Anti-goals (a failure even if the code «works»)

- Any TCP user line stops working because of the procedure.
- The Hub cut off with no way back except VNC.
- A step that silently half-applies (entries on the new address, Hub answering UDP from the old).
- A real address, key or token in the repository.

## Constraints

- Prod and network changes only with the owner's explicit «да»; network/firewall changes auto-revert
  unless confirmed (CLAUDE.md).
- `main` is the only integration branch; deploy only from a clean `main` commit.
- The API runs as root on the Hub (`heyearth-api.service User=root`): it may run `ip`, `netplan`,
  `systemd-run` locally.
- SSH to entries/exits goes through the existing provisioning dial (Hub key / encrypted password, pinned
  host key).
- Admin UI: theme lock (tokens, Geist, hairlines, 36 px rows). Russian UI strings; no protocol names
  beyond what the admin already shows (Берёза, Дуб, Бамбук are fine in the admin).

## Approaches

1. **The wizard does everything, the Hub's own network included — picked (owner, 20.11).**
   Hub-local steps run through `provision.RunHub` + `LocalRemote` (the tgweb pattern), nodes through the
   existing provisioning runner (reprovision of online entries/exits — steps whose Verify passes are not
   touched). Adding an address is additive. The route-source switch is the only risky step: a re-check of
   outbound from the new address, then a `systemd-run` revert timer, then the change, then the owner's
   confirmation from the admin.
2. **Hub network by hand, the rest by the wizard.** The wizard prints `ip`/netplan commands for VNC and
   checks the result. The API can never cut the Hub off, but the change is not «from the admin» and is
   awkward from a phone. Rejected by the owner.
3. **Make the Hub's address invisible to users** (all user names and lines through entries; the review's
   A3 «вход как мини-хаб»). Complementary, not an alternative: the wizard is still needed for the
   entries' upstream and the exits. Left to the architecture review.

## Owner decisions (20.11)

- New address = an additional IP on the same VDSina VPS.
- DNS by hand; the wizard lists the records and waits until public resolvers return the new address.
- The wizard does everything, including the Hub's network, with an auto-revert timer.
- Transition: `Profile-Update-Interval` 1 h and a counter «обновили подписку N из M активных».
- Бамбук: moving it in one step with the entries is acceptable (minutes).

## Design

### Address roles

The wizard page shows every IPv4 address of the Hub's default-route interface with its roles:
«для пользователей» (`main_server_host`), «исходящий» (route source), «шлюз» (the address in the
gateway's prefix — removing it breaks routing), «новый» / «выводится» during a change, «прочий». Today's
Hub: `203.0.113.5` шлюз (filtered upstream, never for users), `192.0.2.10` для пользователей + исходящий,
a dead third address прочий.

### Prerequisite: Дуб's Host independent of the address

New setting `hub_xhttp_host`, seeded by the migration from the current `main_server_host` (so the bytes
of every generated config and subscription stay the same — no xray restart, goldens unchanged). The xray
XHTTP inbound `host`, the JSON/URI/clash Host header and `entries_admin` read `hub_xhttp_host` (fallback
`main_server_host` when absent). The value is an opaque string inside Reality TLS; it never changes
again, so changing `main_server_host` no longer touches Дуб. Ships and deploys before the wizard.

### `hubIPCache` reset

`hubIPs.reset()`; the wizard calls it after adding and after removing an address. The next xray and
hysteria syncs render the new «refuse the Hub's own addresses» list: one xray + one hysteria restart per
reset (seconds; the wizard warns before the step). Exits'/entries' allow lists read it at job start.

### The change record

Table `hub_address_changes`: `id`, `old_ip`, `new_ip`, `iface`, `gateway`, `state`, `steps jsonb`
(per step: status ok/failed/running/skipped, started/finished, short log, check results), `created_by`,
`created_at`, `switched_at`, `confirm_deadline`, `confirmed_at`, `finished_at`, `reverted_at`,
`revert_reason`. At most one change not in a final state (partial unique index). States:
`draft → added → checked → nodes_ready → dns_ready → switching → transition → finished`, plus
`cancelled`, `reverting` and `reverted`. A step may be re-run while its state allows; going back is «Отменить»
(before `switching`) or «Вернуть как было» (in `switching`/`transition`).

Steps run in a background goroutine; the page polls. An API restart mid-step marks the step
interrupted (as provisioning jobs do) and the owner re-runs it.

### Steps

**1. Добавить адрес.** Input: the new IPv4 (public unicast, not a servers row, not already on the Hub).
The wizard shows its /24 and ASN next to the current address's (Team Cymru lookup, as the mobile check
does). Apply: `ip addr add <new>/32 dev <iface>` (runtime) and the address appended to the netplan
`addresses:` of that interface (backup `…bak-hubaddr-<id>`), `netplan generate` (never `apply`).
Verify: the address is on the interface; the netplan file contains it; `netplan generate` passes.
Rollback: `ip addr del`, the backup restored. Then `hubIPs.reset()`.
If VDSina already bound the address (cloud-init), Apply is a no-op and Verify passes.

**2. Проверить адрес** (gate for step 3). Each check is shown with its numbers; the owner can re-run.
- *Outbound from the new address.* From the Hub, connects bound to `<new>` vs bound to the current
  address, 24 tries each with distinct source ports, to: every online entry `:443`, every online exit
  `:443`, `acme-v02.api.letsencrypt.org:443`, `github.com:443`, `api.yookassa.ru:443`, `smtp_host:port`
  (banner). Pass per target: at least one success and not worse than the current address by more than
  20 % of tries (entry 6 loses ~40 % on both addresses — ECMP on the path, not the address); a target
  that fails on the current address too is shown as «не показатель». Catches addresses without a working
  route (the third address today: 0/24).
- *Services that might allow-list the Hub's source* (connect checks cannot see it): bound to `<new>`,
  an SMTP session to `smtp_host` up to a successful AUTH (no message sent) and an authenticated read-only
  YooKassa call (`GET /v3/me` with the shop credentials). Telegram goes through exits and is not
  affected.
- *Inbound from abroad.* Through an exit's SOCKS (`pickExit`, as the entry monitor): TLS to
  `<new>:443` with SNI `hiearth.ru` (site certificate verifies), Reality handshake to `<new>:443` and
  `<new>:2443` with the Reality SNI.
- *Inbound from RU.* Over SSH from every online entry: HTTPS to `<new>:443` with `--resolve hiearth.ru`,
  downloading ≥ 256 KB (the 10.10 failure was «connection opens, data never arrives»; the ~15–20 KB
  freeze threshold). The probe object is a static file the Hub's nginx serves (builder's call where).
- *From a phone on a mobile network.* The mobile-check responder (`mobile_check.go`) started on the Hub
  itself through `LocalRemote`, bound to `<new>` on a free port of 8880/8080 with a temporary firewall
  allow on that address only; same page, 2 MiB blob, `bytes_acked`, operator ASN, QR. The same check can
  be run against the current address for comparison. Required unless the owner explicitly skips it
  (recorded as «не проверено с телефона» in the change and the audit).
- Бамбук cannot be checked yet (hysteria answers from the route source): shown as «после переключения».

**3. Подготовить узлы** (additive; nothing points at the new address yet). Reprovision every online
exit (`firewall` has no Verify and re-applies: TCP 50052 allowed per Hub IP, so `<new>` is added;
`node_agent` guard drifts and re-applies) and every online entry (DNAT/stream/route allow still on the
current address, their Verify passes). **SSH is not covered by the recipes:** both firewall scripts add an
SSH allow only when no rule names the SSH port yet, so a node whose SSH is already restricted to the
Hub's addresses never gets `<new>`. Step 3 therefore runs a new node script: for every ufw / iptables rule
that allows the node's SSH port from one of the Hub's current addresses, add the same rule from `<new>`
(idempotent; nothing when SSH is open to all or restricted otherwise). Verify: SSH and TCP 50052 to each
exit, SSH to each entry, all dialled bound to `<new>`.
A node that fails stops the step with its job link; the owner fixes or retries. Offline nodes are listed
as «не подготовлен» and block step 5 unless the owner excludes them (they would lose the Hub after the
switch).

**4. DNS.** The wizard resolves the known names (`hiearth.ru`, `www`, `admin`, `site`, `cdn`, the host
of `site_url`, `hub_main_server_host`, `sub_domain`, the tgweb domain) on 1.1.1.1, 8.8.8.8, 77.88.8.8
(at least two must answer) and lists every name that returns the current address: «поменяйте A-запись
X на 198.51.100.20 (оранжевое облако не включать)». Names that point at entries or elsewhere are shown,
not touched. Gate: every listed name returns only `<new>` on every answering resolver, and TLS to
`<new>:443` verifies for each name. Re-check button and auto re-check every 30 s while open. The site,
admin and old links on `hiearth.ru` move here (TCP works on both addresses).

**5. Переключить.** The only risky step; the page says what happens and that Бамбук pauses for minutes.
1. Re-run the outbound check of step 2 (it must still pass).
2. Write `/opt/heyearth/state/hubaddr/<id>-revert.sh` (route source back to the current address with
   the route's other attributes, `main_server_host` back, `config_version + 1`, restart tunnel clients,
   a marker file) and arm it: `systemd-run --unit=heyearth-hubaddr-revert-<id> --on-active=600`.
   The timer protects the Hub, so it must outlive the entry jobs: while entry jobs of this step are
   running and progressing, the API re-arms it in 5-minute increments (at most 40 minutes in total);
   once jobs and checks are done, the owner has a fixed 10 minutes to confirm (countdown on the page).
   An API that dies stops re-arming, and the timer fires.
3. Runtime only: `ip route replace default via <gw> dev <iface> src <new> …` (attributes kept from
   `ip -j route show default`). The netplan `from:` is written at confirmation, not now: a reboot in the
   window brings the Hub back on the current source (see «Failure modes»).
4. Restart `hysteria2-client-*` and `heyearth-stls-client-*` so the tunnels come up from the new source
   at once instead of after their idle timeout.
5. `main_server_host = <new>`, `config_version + 1`, transition on (`hub_addr_transition_since`),
   `RequestXraySync` (a no-op for xray now that Дуб's Host is frozen).
6. Reprovision every online entry with `HubIP = <new>` (`relay_rules`, `relay_stream` re-apply;
   `relay_firewall`'s check looks for the ufw route allow to `$HUB`, so the forward allow to `<new>` is
   added; `relay_path` verifies TLS 443/2443 and Бамбук through the entry — i.e. that the Hub now answers
   UDP from the new address).
7. Checks: Бамбук probe from an exit to `<new>` (`bamboo_probe.go`), tunnel health per exit, node-agent
   metrics per exit, entry monitor sees entry 0 on `<new>`.
8. «Подтвердить» is enabled when 6–7 are green (or with an explicit «подтвердить несмотря на …»).
   Confirm = the owner's request reaches the API (proof the admin is reachable after the switch):
   the timer is stopped, the netplan `from:` is written (backup, `netplan generate`), state `transition`.
   «Вернуть как было» runs the revert at once.

Revert (timer fired, button, or the API finding the route source ≠ `<new>` while `switching`): the
script restores the Hub side; the API (immediately, or on start-up via the marker) moves the change to
`reverting`, waits for in-flight entry jobs of the change to end (the runner has no cancel; jobs are
bounded), clears the transition flag, reprovisions the entries back to the current address
and sends an alert to the alerts bot. Entries that fail to re-point are retried every 5 minutes while the
API runs (one alert, not one per retry) and can be retried from the page; the change becomes `reverted`
with the reason once every online entry is back. TCP through entries keeps working throughout a revert
(TCP is answered from the connection's own address); Бамбук through a not-yet-re-pointed entry gets no
answers.

What Бамбук users see after a confirmed switch: through entries it resumes as soon as each entry is
re-pointed (minutes). The Hub-direct Бамбук way (`hub` in the JSON balancer, the single URI Бамбук line of
non-Happ clients) is dead for a client until it refreshes its subscription (1 h interval in the
transition; clients that ignore the interval refresh when the user does). In the JSON subscription the
balancer falls over to the entries' ways, so Happ users notice nothing; URI clients lose Бамбук until a
refresh. Owner accepted this on 20.11.

**6. Переходный период.** While `transition`: `Profile-Update-Interval` is 1 h (`planEntries` gets the
flag, like a failover). The page shows:
- «обновили подписку N из M»: M = users with an active subscription who fetched it in the last 14 days,
  N = those whose last fetch is after `switched_at`. Needs `users.subscription_fetched_at`, updated by
  the subscription handler at most once per 5 minutes per user.
- new connections per minute still arriving at the old address (an nft counter table `heyearth_hubaddr`
  on `ip daddr <old>` for TCP 443/2443, UDP 30000–65000, added at step 5, removed at step 7).
- a recommendation to finish when N/M ≥ 90 % and the old-address counter is near zero, or after 72 h.
The owner decides when.

**7. Вывести старый адрес.** Removing the address ends the TCP «· напрямую» lines of clients that never
refreshed, so the step is gated: it is enabled after 72 h of transition with N/M ≥ 90 % and the
old-address counter under 1 new connection per minute over the last hour; before that it needs an
explicit «вывести, несмотря на …» confirmation that names the numbers (recorded in the audit). Interval
back to normal, transition off. If the old address is not the
gateway carrier and not on any other role: `ip addr del`, removed from netplan (backup, generate), under
the same revert-timer pattern; `hubIPs.reset()`; exits'/entries' allow lists re-rendered without it on
their next reprovision (the wizard offers to run it). If it carries the gateway it stays on the
interface as «шлюз» and only leaves the user-facing roles. The final screen lists the owner's manual
leftovers: `DEPLOY_HOST` in `server/.env`, the VDSina address order, the counter table removed.

### Cancel

«Отменить» before step 5: the address added by step 1 is removed (unless VDSina bound it), the netplan
backup restored, `hubIPs.reset()`; allow lists added in step 3 stay (harmless) and leave on the next
reprovision. DNS changed by the owner in step 4 must be put back by the owner: the wizard says so and
re-checks it.

### API and permissions

- New permission `hub.address` («Адрес хаба: смена») in `permissionCatalog` + migration seeding it
  (super_admin gets it automatically). Read endpoints need `servers.read`.
- `GET /api/admin/hub/addresses` (interface addresses with roles, the active change);
  `POST /api/admin/hub/address-changes` (`{ip}`, starts step 1); `GET …/{id}`;
  `POST …/{id}/steps/{n}` (run / re-run); `POST …/{id}/mobile-check` + `GET` (start / read);
  `POST …/{id}/confirm`, `…/revert`, `…/finish`, `…/cancel`.
- Every action → `WriteAuditLog("hub_address.<action>", "hub_address_change", id, details)`;
  details never contain secrets.
- All network commands are rendered by pure functions (tested) and run through `provision.Remote`
  (`LocalRemote` in prod, a fake in tests).

### Admin UI

«Серверы → Хаб»: a card «Адрес хаба» with the address/role table and «Сменить адрес». The wizard is a
vertical stepper (one step open at a time) with each step's status, numbers of its checks, the job links
of node steps, the countdown of the revert timer in step 5, and the counters of step 6. One primary button
per step. Theme lock applies.

### Failure modes

- API restart mid-step: the step is interrupted; re-run it. In `switching` the timer keeps running in
  systemd and reverts if the owner does not confirm.
- Reboot in `switching`: transient timers die with the reboot, but the netplan file still has the current
  source, so the Hub boots on it; on start-up the API sees `switching` with route source ≠ `<new>` and
  runs the API-side revert (settings, entries).
- An entry's SSH dies during step 5: its job fails; the change stays in `switching`; the owner retries or
  reverts. Until it is re-pointed that entry still forwards TCP to the old address (works) and UDP gets
  no answers.
- The new address dies in the transition (blocked again): run a new change from it; the old one may still
  be on the interface.
- Entry failover in `auto` mode during the change: unaffected (it switches between entries); entry 0
  follows `main_server_host`.

### Testing

- Pure renderers (netplan edit, route replace, revert script, nft counter table, DNS list, outbound
  pass rule) with table tests; RFC 5737 addresses only.
- The state machine with a fake `Remote` and fake node runner: every step's apply/verify/rollback,
  re-runs, cancel, revert by timer marker, revert on start-up.
- Goldens: subscription/xray outputs unchanged by `hub_xhttp_host`; a golden for the 1 h interval in
  transition.
- Postgres-backed tests for the migration (seed of `hub_xhttp_host`, permission, partial unique index)
  and the subscription-fetch counter.
- admin_web: node tests for the step model (which buttons are enabled per state).
- Prod evidence (after the owner's «да»): a dry run of steps 1–3 with the real new address, then the
  switch with the owner watching.

## A caveat for the owner

The architecture review (20.11) notes that mobile operators cut **VDSina's subnet**, not one address: a
new VDSina address in the same subnet may be blocked as well. The wizard's step 2 phone check proves it
before anything moves, and step 1 shows the /24 and ASN. Ask VDSina for an address from a different
range; if every VDSina range is cut, only approach 3 (or another provider) helps mobile users.

## Calls made in the design (not the owner's words — check these)

- Дуб's Host frozen in `hub_xhttp_host` (seeded from today's value) and shipped before the wizard.
- Netplan `from:` written only at confirmation; the switch itself is runtime-only.
- Revert timer: re-armed by the API while entry jobs run (≤ 40 min), then 10 min to confirm.
- Revert is `reverting` with retries until every entry is back, then `reverted`.
- Step 3 adds SSH allows from the new address next to existing Hub-address SSH rules (the recipes would
  not).
- Step 2: authenticated SMTP AUTH and YooKassa probes from the new address; the phone check is required
  unless explicitly skipped.
- Step 7 gated by 72 h, N/M ≥ 90 % and the old-address counter, with an explicit override; an address
  carrying the gateway is never removed from the interface.
- Tunnel clients restarted right after the route switch.
- One xray + hysteria restart at each `hubIPCache` reset (address added, address removed), warned in
  the UI.

## Left to the builder

- Where the 256 KB probe object lives (static file on the site root vs a dedicated location).
- The exact netplan YAML edit (structure-preserving; refuse and stop if the file layout is unexpected).
- Whether nodes are reprovisioned sequentially or with a small parallelism.
- Polling cadence and the shape of the step log.
