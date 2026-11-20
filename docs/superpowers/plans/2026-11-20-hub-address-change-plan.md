# Hub address change wizard — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans (inline, per CLAUDE.md «Экономия токенов»). Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An admin wizard «Хаб → Адрес» that moves everything from the Hub's current public address to an
additional one on the same VPS, proves the new address first, auto-reverts the risky switch, and retires
the old address after a measured transition.

**Architecture:** Pure renderers and checks in a new package `internal/hubnet` (scripts, netplan edit,
probes, verdicts). The change record and its state machine live in `cmd/api/hubaddr_*.go` (they need
the API's probes, node runner and settings). Hub-local steps run through `provision.Remote`
(`LocalRemote` in prod); nodes through the existing runner (`KindReprovision`). Admin UI: a card and a
stepper on «Серверы → Hub».

**Tech Stack:** Go (net, net/netip, gopkg.in/yaml.v3 node API, database/sql + lib/pq), PostgreSQL
migrations, React 19 + Tailwind v4 (admin theme lock), node --test.

**Spec:** `docs/superpowers/specs/2026-11-20-hub-address-change-design.md` (read it first; the plan
argues from it). Beads epic: `heyearthproject-2b6u`.

## Global Constraints

- Addresses in code, tests, docs: RFC 5737 only (`192.0.2.x`, `198.51.100.x`, `203.0.113.x`). No secrets.
- Next free migration: `000094`. Migrations run at API start; every `.up.sql` has a `.down.sql`.
- Permission key `hub.address` (Name «Hub Address», Description «Change the Hub's public address:
  add, check, switch with auto-revert, retire»): in `permissionCatalog` and seeded by migration.
- Audit actions `hub_address.<action>`, target type `hub_address_change`; details never contain secrets.
- Never `netplan apply`; only `netplan generate` after edits, each edit with a backup
  `<file>.bak-hubaddr-<id>`.
- Revert timer: `systemd-run --unit=heyearth-hubaddr-revert-<id> --on-active=…`; re-armed in 5-minute
  steps while entry jobs run, at most 40 minutes after arming; then 600 s for the owner to confirm.
- Outbound pass rule: per target, `new ≥ 1` and `new ≥ cur − 0.2·tries`; a target with `cur = 0` is
  «не показатель» (not a failure). Tries = 24.
- DNS resolvers `1.1.1.1`, `8.8.8.8`, `77.88.8.8`; at least two must answer.
- Step 7 gate: 72 h in transition, N/M ≥ 0.90, old-address counter < 1 new connection/min over the last
  hour; otherwise only with an explicit override that names the numbers.
- Transition `Profile-Update-Interval` = 1 h; progress window M = active subscription and a fetch in the
  last 14 days; `subscription_fetched_at` written at most once per 5 minutes per user.
- Admin UI: theme tokens only, Geist / Geist Mono for IPs and numbers, hairline borders, no shadows, one
  primary button per step, Russian strings.
- Prod: nothing is deployed or switched without the owner's explicit «да»; deploy only from a clean
  `main` commit.

## Review Focus

1. Netplan file with an unexpected layout (no `ethernets.<iface>.addresses`, an address list in flow
   style, several default routes) → the edit refuses with a clear error and changes nothing.
2. A second «Сменить адрес» while a change is active, or two step runs at once → refused (one active
   change; a per-change mutex), never two concurrent mutations.
3. API restart or reboot while `switching` → on start-up the watcher reverts if the route source is not
   the new address, and resumes re-arm/confirm if it is.
4. A node offline or with an unconfirmed host key during step 3/5 → listed, blocks the switch unless
   excluded; never silently skipped.
5. The entered address equals a relay/exit address, the gateway carrier, the current address, a private
   or IPv6 address → refused at step 1 with the reason.

---

### Task 1: Freeze Дуб's Host header (`hub_xhttp_host`) — ships alone first

**Files:**
- Create: `server/backend/migrations/000094_hub_xhttp_host.up.sql`, `.down.sql`
- Modify: `cmd/api/client_api.go` (`hubConnectionInputs`, `loadHubSettingInputs`, xhttpURI call sites
  at ~387, ~434, ~620), `cmd/api/subscription_json.go:178`, `cmd/api/subscription_handler.go`
  (`clashXHTTPProxy` callers ~359, ~406, ~421), `cmd/api/xray_sync.go` (settings struct ~300, read ~347,
  inbound `host` ~542), `cmd/api/entries_admin.go` (preview inputs)
- Test: `cmd/api/subscription_xhttp_host_test.go`, existing goldens

**Interfaces:**
- Produces: `hubConnectionInputs.xhttpHost string`; xray settings `xhttpHost string`; setting key
  `hub_xhttp_host`.

- [ ] **Step 1: Failing test** `TestXHTTPHostSurvivesMainHostChange`: inputs with
  `mainServerHost="198.51.100.20"`, `xhttpHost="192.0.2.10"` → every Дуб URI has `host=192.0.2.10`, the
  JSON XHTTP outbound `xhttpSettings.host == "192.0.2.10"`, clash `xhttp-opts.host: 192.0.2.10`; and
  `TestXHTTPHostDefaultsToMainHost`: `hub_xhttp_host` absent → `xhttpHost == mainServerHost`. Xray build
  test: settings with `xhttpHost="192.0.2.10"`, `mainServerHost="198.51.100.20"` → inbound
  `vless-xhttp` `xhttpSettings.host == "192.0.2.10"`.
- [ ] **Step 2:** `go test ./cmd/api/ -run 'XHTTPHost'` → FAIL (field missing).
- [ ] **Step 3:** Add the field and setting read (default = `mainServerHost` when absent/blank, same in
  `xray_sync`); replace every Host-header argument that is `mainServerHost` with `xhttpHost`. Migration:
  `INSERT INTO app_settings(key,value,updated_at) SELECT 'hub_xhttp_host', value, now() FROM app_settings
  WHERE key='main_server_host' AND btrim(value) <> '' ON CONFLICT (key) DO NOTHING;` down: delete the key.
- [ ] **Step 4:** `go test ./cmd/api/` → PASS, goldens unchanged (`git diff --stat cmd/api/testdata` empty).
- [ ] **Step 5:** `go vet ./cmd/api/ && go build ./...`; commit `feat(subscription): Дуб's Host header no
  longer follows main_server_host`.

### Task 2: `internal/hubnet` — Hub network renderers

**Files:**
- Create: `internal/hubnet/netplan.go`, `internal/hubnet/scripts.go`, tests `netplan_test.go`,
  `scripts_test.go`

**Interfaces:**
- Produces:
  - `type DefaultRoute struct { Gateway netip.Addr; Dev string; Src netip.Addr; Metric int; Proto string; Onlink bool }`
  - `ParseDefaultRoute(ipJSON []byte) (DefaultRoute, error)` — from `ip -j -4 route show default`;
    error when 0 or >1 default routes.
  - `NetplanAddAddress(doc []byte, iface, cidr string) ([]byte, bool, error)` (changed=false when present)
  - `NetplanRemoveAddress(doc []byte, iface, cidr string) ([]byte, bool, error)`
  - `NetplanSetDefaultFrom(doc []byte, iface string, src netip.Addr) ([]byte, bool, error)` — the
    `routes:` entry with `to: default` (or `0.0.0.0/0`) gets `from: <src>`; error if none or several.
  - `AddrAddScript(iface string, a netip.Addr) string`, `AddrDelScript(iface string, a netip.Addr) string`
    (idempotent: check `ip -o -4 addr show dev` first)
  - `RouteReplaceScript(r DefaultRoute, src netip.Addr) string` (keeps metric/proto/onlink)
  - `NetplanWriteScript(path string, content []byte, backup string) string` (backup once, write,
    `netplan generate`, restore backup and exit 1 on failure)
  - `type RevertParams struct { ChangeID int64; Route DefaultRoute; OldSrc netip.Addr; OldMain string; Marker string }`
  - `RevertScript(p RevertParams) string` (route back, `main_server_host` back + `config_version+1` via
    `runuser -u postgres -- psql -d heyearth`, `systemctl restart 'hysteria2-client-*' 'heyearth-stls-client-*'`,
    `touch <Marker>`)
  - `ArmTimerScript(unit, script string, secs int) string`, `DisarmTimerScript(unit string) string`
    (stop timer+service, `reset-failed`; arm = disarm then `systemd-run --unit=<unit> --on-active=<secs>s /bin/sh <script>`)
  - `RestartTunnelsScript() string`
  - `CounterTableScript(old netip.Addr) string`, `CounterReadScript() string`, `ParseCounter(out string) (uint64, error)`,
    `CounterDropScript() string` — nft table `inet heyearth_hubaddr`, counter `old_new` on
    `ip daddr <old> ct state new` for tcp 443/2443 and udp 30000-65000.
  - `NodeSSHAllowScript(sshPort int, hubIPs []netip.Addr, add netip.Addr) string` — ufw: for each rule
    `<sshPort> ALLOW IN <hubIP>` add `ufw allow from <add> to any port <sshPort> proto tcp`; iptables
    INPUT: for each `-s <hubIP>/32 … --dport <sshPort> -j ACCEPT` add the same with `-s <add>`; prints
    `SSH_ALLOW_ADDED` / `SSH_ALLOW_NOT_NEEDED`; never touches rules of other sources.

- [ ] **Step 1: Failing tests** (table tests, RFC 5737):
  `TestNetplanAddAddress` — VDSina-shaped doc (`network.ethernets.ens3.addresses: [203.0.113.5/24, 192.0.2.10/32]`,
  `routes: [{to: default, via: 203.0.113.1, from: 192.0.2.10}]`) + `198.51.100.20/32` → present once,
  comments and key order kept, second call `changed=false`; flow-style list, missing iface, missing
  `addresses` → error. `TestNetplanSetDefaultFrom` — `from` replaced; two default routes → error.
  `TestParseDefaultRoute` — one route parsed; zero/two → error. `TestRouteReplaceScript` contains
  `ip route replace default via 203.0.113.1 dev ens3 src 198.51.100.20 metric 100 proto static`.
  `TestRevertScriptRestoresSourceAndSetting` — contains the route back to `192.0.2.10`, `UPDATE app_settings`
  for `main_server_host` = `'192.0.2.10'`, the marker touch; quoting safe for a host value with a quote.
  `TestNodeSSHAllowScript` — `bash -n` passes; with fake `ufw status` output containing
  `22/tcp ALLOW IN 192.0.2.10` the script adds `198.51.100.20` (run in a temp dir with stub `ufw`/`iptables`
  on PATH where the repo's existing script tests do so; otherwise assert the rendered commands).
- [ ] **Step 2:** `go test ./internal/hubnet/` → FAIL.
- [ ] **Step 3:** Implement. Netplan edit with `gopkg.in/yaml.v3` `yaml.Node` (add to go.mod if absent),
  encoding with indent 2; refuse (error) on anything unexpected.
- [ ] **Step 4:** `go test ./internal/hubnet/` → PASS; `go vet ./internal/hubnet/`.
- [ ] **Step 5:** Commit `feat(hubnet): renderers for the Hub address change`.

### Task 3: `internal/hubnet` — checks and verdicts

**Files:**
- Create: `internal/hubnet/probe.go`, `internal/hubnet/dns.go`, `internal/hubnet/roles.go`, tests

**Interfaces:**
- Produces:
  - `type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)`
  - `BoundDialer(bind netip.Addr, timeout time.Duration) DialFunc`
  - `ProbeTCP(ctx context.Context, dial DialFunc, target string, tries int) int` (successful connects)
  - `type TargetResult struct { Target string; Cur, New, Tries int; Verdict string }` — Verdict
    `ok` | `fail` | `not_indicative`
  - `OutboundVerdict(cur, new, tries int) string`
  - `ProbeSMTPAuth(ctx context.Context, dial DialFunc, host string, port int, user, pass string) error`
    (EHLO, STARTTLS when offered or implicit TLS on 465, AUTH PLAIN/LOGIN, QUIT; no MAIL FROM)
  - `type Resolve func(ctx context.Context, server, name string) ([]netip.Addr, error)` and
    `GoResolve` (net.Resolver PreferGo, dial `udp` to `server:53`, 3 s)
  - `type DNSRow struct { Name string; ByServer map[string][]string; PointsAtOld, Converged bool; Note string }`
  - `CheckDNS(ctx context.Context, r Resolve, names []string, servers []string, old, new netip.Addr) (rows []DNSRow, answered int)`
  - `DNSGate(rows []DNSRow, answered int) error` (nil when ≥2 servers answered and every row that pointed
    at old resolves only to new)
  - `type AddrRole struct { Addr netip.Addr; Prefix int; Roles []string }` roles
    `users` | `source` | `gateway` | `new` | `retiring` | `other`
  - `Roles(ifaceAddrs []netip.Prefix, route DefaultRoute, mainHost, newIP, oldIP string) []AddrRole`
  - `ValidateNew(a string, hubAddrs []netip.Prefix, nodeIPs []string, route DefaultRoute, cur string) (netip.Addr, error)`
    — refuses non-IPv4, private/loopback/link-local, a node address, the current address, the gateway
    carrier (address whose prefix contains the gateway); Russian error texts.

- [ ] **Step 1: Failing tests:** `TestOutboundVerdict` table: (24,24)→ok, (14,10)→ok, (14,9)→fail,
  (24,0)→fail, (0,0)→not_indicative, (0,5)→ok. `TestProbeTCPCountsSuccesses` with a fake dial failing
  every 3rd try → 16 of 24. `TestProbeSMTPAuth` against an in-process fake SMTP server (net.Listener)
  accepting AUTH PLAIN → nil; rejecting → error containing the server's reply. `TestCheckDNS`: fake
  resolve where 1.1.1.1 and 8.8.8.8 return new, 77.88.8.8 errors → answered=2, gate nil; one server still
  old → gate error naming the name and server; a name pointing at an entry → `PointsAtOld=false`, shown.
  `TestRoles`: gateway carrier `203.0.113.5/24` with gw `203.0.113.1`, `192.0.2.10` users+source.
  `TestValidateNew`: each refusal of Review Focus #5 with its reason; `198.51.100.20` accepted.
- [ ] **Step 2:** `go test ./internal/hubnet/` → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** `go test ./internal/hubnet/` → PASS.
- [ ] **Step 5:** Commit `feat(hubnet): outbound, SMTP and DNS checks, address roles`.

### Task 4: Change record, permission, subscription fetch column (migration + store)

**Files:**
- Create: `migrations/000095_hub_address_change.up.sql`, `.down.sql`; `cmd/api/hubaddr_store.go`,
  `cmd/api/hubaddr_store_test.go` (Postgres-backed, `testpg.Open`)
- Modify: `internal/admin/auth.go` (`permissionCatalog` + comment «Also seeded by migration 000095»)

**Interfaces:**
- Produces:
  - Table `hub_address_changes` (columns as in the spec «The change record»), `CHECK (state IN (…))`,
    partial unique index `ON ((true)) WHERE state NOT IN ('finished','cancelled','reverted')`.
  - `users.subscription_fetched_at TIMESTAMPTZ` + index on it.
  - Permission row `hub.address` granted to `super_admin`.
  - `type hubAddrChange struct { ID int64; OldIP, NewIP, Iface, Gateway, State string; Steps map[string]hubAddrStep; CreatedBy string; CreatedAt time.Time; SwitchedAt, ConfirmDeadline, ConfirmedAt, FinishedAt, RevertedAt *time.Time; RevertReason string; PhoneSkipped bool }`
  - `type hubAddrStep struct { Status string; StartedAt, FinishedAt *time.Time; Log string; Checks json.RawMessage }`
    Status `pending|running|ok|failed|skipped|interrupted`.
  - States: const `hubAddrDraft="draft"`, `added`, `checked`, `nodes_ready`, `dns_ready`, `switching`,
    `transition`, `finished`, `cancelled`, `reverting`, `reverted`.
  - `hubAddrNext(from, to string) bool` — allowed transitions only.
  - `createHubAddrChange(ctx, db *sql.DB, c hubAddrChange) (int64, error)` — returns `errHubAddrActive`
    on the unique index.
  - `getHubAddrChange(ctx, db, id) (hubAddrChange, error)`, `activeHubAddrChange(ctx, db) (hubAddrChange, bool, error)`
  - `setHubAddrState(ctx, db, id int64, from, to string, set func(*hubAddrChange)) error` — `UPDATE … WHERE id=$1 AND state=$2`
    (optimistic), refuses transitions `hubAddrNext` rejects.
  - `saveHubAddrStep(ctx, db, id int64, name string, s hubAddrStep) error` (jsonb_set).
  - `markHubAddrInterrupted(ctx, db) (int, error)` — steps `running` → `interrupted` (API start-up).

- [ ] **Step 1: Failing tests** (`TEST_DATABASE_URL` set): create → second create `errHubAddrActive`;
  after `cancelled` a new create succeeds; `setHubAddrState` with a stale `from` → error, with a
  forbidden transition (`draft→switching`) → error; `saveHubAddrStep` round-trips; interrupted marking;
  the permission row exists and `super_admin` has it; `users.subscription_fetched_at` exists.
- [ ] **Step 2:** `go test ./cmd/api/ -run HubAddrStore` → FAIL.
- [ ] **Step 3:** Implement migration + store + catalog line.
- [ ] **Step 4:** PASS; `go test ./internal/admin/` still PASS.
- [ ] **Step 5:** Commit `feat(hub-address): change record, permission hub.address`.

### Task 5: Transition in subscriptions (interval, fetch time, progress)

**Files:**
- Create: `cmd/api/hubaddr_transition.go`, `cmd/api/hubaddr_transition_test.go`
- Modify: `cmd/api/client_api.go:loadEntryPlan` (apply transition), `cmd/api/subscription_handler.go`
  (touch after the token resolves to `userID`, non-blocking: errors logged)

**Interfaces:**
- Produces:
  - setting `hub_addr_transition_since` (RFC 3339; absent/blank = off)
  - `applyHubTransition(p entryPlan, active bool) entryPlan` — `interval = 1` when active. Pure.
  - `touchSubscriptionFetch(ctx, db *sql.DB, userID string) error` —
    `UPDATE users SET subscription_fetched_at=now() WHERE user_id=$1 AND (subscription_fetched_at IS NULL OR subscription_fetched_at < now()-interval '5 minutes')`
  - `subscriptionRefreshProgress(ctx, db, since time.Time) (n, m int, err error)` — M: users with an
    active subscription (same predicate as the subscription cron's «active») and `subscription_fetched_at > now()-14 days`;
    N: those of M with `subscription_fetched_at > since`.

- [ ] **Step 1: Failing tests:** `TestApplyHubTransition` (4→1 when active; pinned plan keeps 1 when
  active; inactive unchanged). Golden-style test: subscription headers with transition on →
  `Profile-Update-Interval: 1`. PG: two touches within 5 min → one update (compare timestamps);
  progress with 3 active users (fetched before since, after since, never) → n=1, m=2.
- [ ] **Step 2:** FAIL. **Step 3:** Implement. **Step 4:** `go test ./cmd/api/` PASS.
- [ ] **Step 5:** Commit `feat(hub-address): hourly refresh and fetch progress during the transition`.

### Task 6: Engine, steps 1–2 (add the address, check it) + cache reset + phone check on the Hub

**Files:**
- Create: `cmd/api/hubaddr_engine.go`, `cmd/api/hubaddr_steps_add.go`, `cmd/api/hubaddr_steps_check.go`,
  tests `hubaddr_engine_test.go`, `hubaddr_steps_check_test.go`
- Modify: `cmd/api/hub_addrs.go` (`func (c *hubIPCache) reset()`), `internal/admin/mobile_check.go`
  (export `MobileCheckStartScript(token string, secs int, bind string) string` — `bind==""` keeps
  today's behaviour; with a bind the responder listens on `bind` only, skips port 80, and the temporary
  ufw rule is `ufw allow to <bind> port <p> proto tcp`; export `ParseMobileCheckLog` and the read script
  `GetMobileCheckHandler` runs today as `MobileCheckReadScript(token string) string`)

**Interfaces:**
- Consumes: Task 2–5 functions; `provision.Remote`, `provision.LocalRemote`; `entryMon.pickExit`,
  `entryTLSCheck(socksPort, host, port, sni)`, `hubOwnIPs`.
- Produces:
  - `type hubAddrEnv struct { DB *sql.DB; Hub provision.Remote; Nodes admin.Provisioner; Jobs admin.NodeJobStore; DialNode func(provision.Node) (provision.Remote, error); Now func() time.Time; Sleep func(context.Context, time.Duration) error; Bound func(netip.Addr) hubnet.DialFunc; Resolve hubnet.Resolve; PickExit func() (int, error); TLSCheck func(socks int, host string, port int, sni string) error; Bamboo func(socks int, host string) error; Setting func(key, def string) (string, error); SetSetting func(key, val string) error; ResetHubIPs func(); Alert func(key, text string) }`
    — every side effect is a field; `defaultHubAddrEnv()` wires prod.
  - `type hubAddrEngine struct { env hubAddrEnv; mu sync.Mutex }` (one mutation at a time) and
    `var hubAddr *hubAddrEngine`
  - `(e *hubAddrEngine) Start(ctx, ip, admin string) (int64, error)` — validates (Task 3 `ValidateNew`),
    creates the record, runs step `add` in the background.
  - `(e *hubAddrEngine) RunStep(ctx, id int64, step string) error` — steps `add|check|nodes|dns|switch|finish`;
    refuses when the state does not allow it or a step is running (`errHubAddrBusy`).
  - Step `add`: Apply/Verify/Rollback per spec step 1 via `env.Hub`; then `env.ResetHubIPs()`; state
    `draft→added`.
  - Step `check`: writes the 256 KiB probe file `/var/www/heyearth-site/hub-probe.bin` (random bytes,
    `PutFile`, 0644); outbound table (targets: online relays `:443`, online exits `:443`,
    `acme-v02.api.letsencrypt.org:443`, `github.com:443`, `api.yookassa.ru:443`, `smtp_host:smtp_port`),
    SMTP AUTH and YooKassa `GET /v3/me` bound to new; inbound via exit (TLS SNI `hiearth.ru` verified,
    Reality on 443/2443); from each online relay over SSH:
    `curl -sS --resolve hiearth.ru:443:<new> -o /dev/null -w '%{http_code} %{size_download}' https://hiearth.ru/hub-probe.bin`
    (pass: `200 262144`); phone result read from the Hub responder. Gate to `checked`: outbound all
    ok/not_indicative, abroad ok, ≥1 relay ok, phone ok or `PhoneSkipped`.
  - `(e *hubAddrEngine) StartPhoneCheck(ctx, id int64, which string) (url, qr string, expires time.Time, err error)`
    (`which` = `new|current`), `PhoneCheck(ctx, id) ([]admin.MobileCheckResult, error)`, `SkipPhone(ctx, id) error`.
- [ ] **Step 1: Failing tests** with a fake Remote recording scripts and a fake env:
  `TestStartRefusesSecondChange`, `TestRunStepRefusedWhileRunning` (`errHubAddrBusy`), `TestAddStepAppliesAndVerifies` (scripts contain `ip addr add 198.51.100.20/32 dev ens3`,
  netplan written with backup, `ResetHubIPs` called once), `TestAddStepRollbackOnVerifyFail`
  (address removed, backup restored), `TestCheckGate` (outbound fail → stays `added`; all green + phone
  skipped → `checked`, `PhoneSkipped=true`), `TestHubIPCacheReset` (after reset the next get recomputes),
  `TestMobileCheckStartScriptBind` (bind set → no port 80, `ufw allow to 198.51.100.20 port`).
- [ ] **Step 2:** FAIL. **Step 3:** Implement (start-up hook `markHubAddrInterrupted` in `main.go`).
- [ ] **Step 4:** `go test ./cmd/api/ ./internal/admin/` PASS.
- [ ] **Step 5:** Commit `feat(hub-address): add and check steps`.

### Task 7: Steps 3–4 (prepare nodes, DNS)

**Files:**
- Create: `cmd/api/hubaddr_steps_nodes.go`, `cmd/api/hubaddr_steps_dns.go`, tests

**Interfaces:**
- Consumes: `env.Nodes.Start(ctx, serverID, provision.KindReprovision)`, `env.Jobs.JobView`,
  `hubnet.NodeSSHAllowScript`, `hubnet.BoundDialer`, `hubnet.CheckDNS`/`DNSGate`.
- Produces:
  - `reprovisionAll(ctx, env, role string, ids []int) []hubAddrNodeResult` —
    `type hubAddrNodeResult struct { ServerID int; Name, IP string; JobID int64; Status, Error string }`;
    sequential, waits for each job (poll `JobView` every 3 s, 15 min cap per job).
  - Step `nodes`: online exits and relays reprovisioned; then per node `NodeSSHAllowScript` over
    `DialNode`; Verify per node: SSH (TCP to the SSH port) and, for exits, TCP 50052, both via
    `env.Bound(new)`. Nodes not online or with an unconfirmed host key → result `not_ready`; the step is
    `ok` only when every node is ok or excluded (`excluded []int` stored in the step's checks via
    `ExcludeNode(ctx, id, serverID)`). State `checked→nodes_ready`.
  - Step `dns`: names = `hiearth.ru, www.hiearth.ru, admin.hiearth.ru, site.hiearth.ru, cdn.hiearth.ru`
    ∪ host of `site_url` ∪ `hub_main_server_host` ∪ `sub_domain` ∪ the tgweb domain setting (deduped,
    blanks skipped); rows + gate; TLS to `<new>:443` with `ServerName=<name>` verifies for each row that
    pointed at old. State `nodes_ready→dns_ready`. The page polls `RunStep(dns)` every 30 s while open.
- [ ] **Step 1: Failing tests:** `TestNodesStepWaitsForJobs` (fake runner: job running→succeeded),
  `TestNodesStepFailedJobStops` (state stays, result names the job), `TestNodesStepOfflineBlocksUnlessExcluded`,
  `TestNodesSSHAllowRunsPerNode`, `TestDNSStepNames` (dedup, `go.` pointing elsewhere shown, not required),
  `TestDNSStepGate`.
- [ ] **Step 2:** FAIL. **Step 3:** Implement. **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(hub-address): prepare nodes and DNS steps`.

### Task 8: Step 5 — switch, revert timer, confirm, revert

**Files:**
- Create: `cmd/api/hubaddr_switch.go`, `cmd/api/hubaddr_watch.go`, tests
- Modify: `cmd/api/main.go` (start `startHubAddrWatcher()` after DB init, before HTTP serve)

**Interfaces:**
- Consumes: `hubnet.ParseDefaultRoute`, `RouteReplaceScript`, `RevertScript`, `ArmTimerScript`,
  `DisarmTimerScript`, `RestartTunnelsScript`, `CounterTableScript`, `NetplanSetDefaultFrom`,
  `NetplanWriteScript`, `reprovisionAll`, `env.Bamboo`, `env.TLSCheck`.
- Produces:
  - Paths: revert script `/opt/heyearth/state/hubaddr/<id>-revert.sh`, marker
    `/opt/heyearth/state/hubaddr/<id>-reverted`; unit `heyearth-hubaddr-revert-<id>`.
  - Step `switch` order exactly as spec step 5 items 1–7: re-check outbound → write + arm (600 s) →
    `RouteReplaceScript` (runtime) → `RestartTunnelsScript` → settings `main_server_host=<new>`,
    `config_version+1`, `hub_addr_transition_since=now` (the 1 h interval starts at the switch; a revert
    clears it) → counter table →
    `reprovisionAll(relays)` with re-arm every 2 min to `now+5 min` while jobs run, never past
    `armedAt+40 min` → checks (Bamboo via exit to new, TLS 443/2443 via exit to new, each exit's tunnel
    health from `servers.tunnel_status`, entry monitor entry 0 address = new) → `confirm_deadline = now+600 s`,
    re-armed to that deadline. State `dns_ready→switching` at the start.
  - `(e *hubAddrEngine) Confirm(ctx, id int64, force bool) error` — only in `switching`, checks green or
    `force`; disarm; netplan `from: <new>` written; state `transition`; `confirmed_at`.
  - `(e *hubAddrEngine) Revert(ctx, id int64, reason string) error` — runs the revert script now (if the
    marker is absent), then `apiRevert`.
  - `apiRevert`: state `switching→reverting`; waits for running entry jobs of the change to end (no job
    cancel API exists); `reprovisionAll(relays)` (they read `main_server_host`, already old); failed
    entries retried every 5 min by the watcher; one alert `hub-address-revert`; when all ok →
    `reverted`, `reverted_at`, `revert_reason`.
  - Watcher `startHubAddrWatcher()` every 30 s and once at start: active change in `switching` → if the
    marker exists or `ParseDefaultRoute().Src != new` → `apiRevert(reason)`; if the deadline passed and
    the timer unit is gone (reboot) → same. In `reverting` → retry failed entries.
- [ ] **Step 1: Failing tests** (fake env, fake clock): `TestSwitchOrder` (recorded script/settings order
  equals the list above; `netplan` untouched until confirm), `TestSwitchRearmsWhileJobsRun` (three 2-min
  ticks → three arm scripts; capped at 40 min), `TestConfirmRequiresGreenOrForce`,
  `TestConfirmDisarmsAndPersistsNetplan`, `TestWatcherRevertsOnMarker`, `TestWatcherRevertsOnSourceMismatchAfterReboot`,
  `TestRevertRetriesFailedEntries` (one alert only), `TestRevertClearsTransition`.
- [ ] **Step 2:** FAIL. **Step 3:** Implement. **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(hub-address): switch with auto-revert, confirm, revert`.

### Task 9: Steps 6–7 (transition view, retire) and cancel

**Files:**
- Create: `cmd/api/hubaddr_finish.go`, tests

**Interfaces:**
- Produces:
  - `(e *hubAddrEngine) Transition(ctx, id) (hubAddrTransitionView, error)` —
    `type hubAddrTransitionView struct { Since time.Time; Hours float64; N, M int; OldPerMin float64; Ready bool; Blockers []string }`;
    `OldPerMin` from two counter reads 60 s apart cached for the page (store last read + time in the
    step's checks); `Ready` per the Global Constraints gate.
  - `(e *hubAddrEngine) Finish(ctx, id int64, override bool, admin string) error` — refuses when not
    `Ready` and not `override`; clears `hub_addr_transition_since`; drops the counter table; if the old
    address has no `gateway` role: `AddrDelScript` + `NetplanRemoveAddress` under the timer pattern
    (arm 600 s, apply, disarm when `ip -o addr` no longer shows it and the Hub still reaches the DB +
    one exit via TLS check), `ResetHubIPs`; state `finished`. Returns the leftovers list (Russian):
    `DEPLOY_HOST в server/.env`, «откажитесь от адреса у VDSina» (only when removed), «переустановите
    выходы и входы, чтобы убрать старый адрес из разрешений» (offered as a button: `reprovisionAll`).
  - `(e *hubAddrEngine) Cancel(ctx, id int64) error` — states `draft..dns_ready` only; removes the
    address if step `add` added it (`Steps["add"].Checks.added == true`), restores the netplan backup,
    `ResetHubIPs`; state `cancelled`; returns «верните DNS-записи, если меняли» when step `dns` ran.
- [ ] **Step 1: Failing tests:** `TestFinishGate` (each blocker text; override passes and is recorded),
  `TestFinishKeepsGatewayCarrier` (no `ip addr del`), `TestFinishRemovesExtraAddress`,
  `TestCancelRemovesOnlyWhatWeAdded`, `TestCancelRefusedWhileSwitching`.
- [ ] **Step 2:** FAIL. **Step 3:** Implement. **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(hub-address): transition view, retire, cancel`.

### Task 10: Admin API

**Files:**
- Create: `cmd/api/hubaddr_admin.go`, `cmd/api/hubaddr_admin_test.go`
- Modify: `cmd/api/main.go` (call `registerHubAddrAdminRoutes()` next to `registerEntryAdminRoutes()`)

**Interfaces:**
- Produces (JSON; errors `{"error": "…"}`):
  - `GET /api/admin/hub/addresses` (`servers.read`) → `{ "addresses": [AddrRole…], "route": {gateway, dev, src}, "change": <view|null> }`
  - `POST /api/admin/hub/address-changes` (`hub.address`) `{ "ip": "…" }` → `{ "id": n }` (409 when active)
  - `GET /api/admin/hub/address-changes/{id}` (`servers.read`) → change view: state, steps (status,
    times, log, checks), `confirm_deadline`, transition view when in `transition`
  - `POST …/{id}/steps/{add|check|nodes|dns|switch}` (`hub.address`) → 202
  - `POST …/{id}/phone-check` `{ "which": "new|current" }` → `{url, qr, expires_at}`; `GET …/{id}/phone-check`;
    `POST …/{id}/phone-skip`; `POST …/{id}/exclude-node` `{ "server_id": n }`
  - `POST …/{id}/confirm` `{ "force": bool }`, `…/revert`, `…/finish` `{ "override": bool }`, `…/cancel`
  - Every POST → `admin.WriteAuditLog(r, "hub_address."+action, "hub_address_change", id, details)`.
- [ ] **Step 1: Failing tests** (httptest + fake engine interface): permission enforced per route
  (403 without `hub.address`), 409 on second create, unknown step 404, audit called with the action.
- [ ] **Step 2:** FAIL. **Step 3:** Implement (the handlers call an interface `hubAddrAPI` that
  `*hubAddrEngine` satisfies). **Step 4:** PASS; `go build ./...`.
- [ ] **Step 5:** Commit `feat(hub-address): admin API`.

### Task 11: Admin UI — «Адрес хаба» card and the wizard

**Files:**
- Create: `admin_web/src/components/servers/HubAddressCard.jsx`,
  `admin_web/src/components/servers/HubAddressWizard.jsx`,
  `admin_web/src/components/servers/hubAddressModel.js`, `hubAddressModel.test.js`
- Modify: `admin_web/src/components/servers/HubPanel.jsx` (mount the card), `admin_web/src/api/admin.js`
  (functions for the routes of Task 10), `admin_web/src/i18n` only if HubPanel strings live there

**Interfaces:**
- Produces: `stepView(change) → [{ key, title, status, primary: {label, action} | null, canRun }]`,
  `roleLabel(role)` (`users`→«для пользователей», `source`→«исходящий», `gateway`→«шлюз»,
  `new`→«новый», `retiring`→«выводится», `other`→«прочий»), `countdown(deadline, now) → "мм:сс"`,
  `transitionBlockers(view) → [string]`.
- UI per spec «Admin UI»: table of addresses (Geist Mono IPs, role chips), «Сменить адрес» (needs
  `hub.address`), stepper with one open step; step 2 shows the outbound table (target, текущий, новый,
  verdict), abroad/RU/phone rows and the QR; step 3 nodes with job links (`/servers?node=<id>`); step 4
  the records to change and per-resolver results; step 5 a warning «Бамбук прервётся на несколько минут»,
  the countdown, «Подтвердить» / «Вернуть как было»; step 6 N/M, old-address per minute, blockers;
  step 7 leftovers. Dialogs via `ConfirmDialog`, never `window.confirm`.
- [ ] **Step 1: Failing tests** (`node --test`): `stepView` per state (only the current step has a
  primary; `switching` → primary «Подтвердить» disabled until checks green, secondary revert),
  `roleLabel`, `countdown(… 125 s) === "02:05"`, `transitionBlockers` texts.
- [ ] **Step 2:** `cd admin_web && npm test` → FAIL. **Step 3:** Implement.
- [ ] **Step 4:** `npm test` PASS; `npm run build` PASS; grep for theme-lock STOP catches
  (`slate-|gray-|zinc-|shadow-|rounded-2xl|rounded-3xl|Inter|Roboto`) in the new files → none.
- [ ] **Step 5:** Commit `feat(admin): Hub address card and change wizard`.

### Task 12: Runbook, docs, verification, merge

**Files:**
- Create: `docs/runbooks/hub-address-change.md` (owner-facing: before buying the IP — ask VDSina for a
  different range; the seven steps; what each check means; revert; VNC fallback commands for the
  owner: `ip route replace default via <gw> dev <iface> src <old>`, `systemctl start heyearth-hubaddr-revert-<id>`)
- Modify: `CLAUDE.md` (Gotchas: «Hub address: change only through «Серверы → Hub → Адрес хаба»;
  Дуб's Host is `hub_xhttp_host`, never follows `main_server_host`»; migrations count 95; admin pages
  unchanged), `docs/runbooks/deploy-and-verify.md` (link)

- [ ] **Step 1:** Write the runbook and doc lines.
- [ ] **Step 2:** Full verification: `cd server/backend && go build ./... && go vet ./cmd/... ./internal/... && go test ./cmd/... ./internal/...`
  (with `TEST_DATABASE_URL` for the PG tests — report if unavailable); `cd admin_web && npm test && npm run build`.
- [ ] **Step 3:** One code review of the branch (`git diff main...HEAD`, reviewer `model: sonnet`), fix
  findings, re-run Step 2.
- [ ] **Step 4:** Merge `main` into the branch, re-run Step 2, fast-forward `main`. Close Beads tasks.
- [ ] **Step 5:** Ask the owner before any deploy. Release 1 (Task 1 alone) may go first: after deploy,
  `curl` a subscription and confirm the Дуб Host is unchanged; xray not restarted
  (`systemctl show xray -p ActiveEnterTimestamp`).
