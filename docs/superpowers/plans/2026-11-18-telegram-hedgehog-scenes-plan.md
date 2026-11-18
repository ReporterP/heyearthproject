# Hedgehog Scenes (Telegram bot + channel) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. In this repo CLAUDE.md overrides: execute inline (executing-plans), one review per phase.

**Goal:** Nine animated hedgehog scenes sent with the bot's event messages and, optionally, with news posts in the channel and the bot.

**Architecture:** A Python + Pillow script draws the site hedgehog's geometry plus a prop per scene into GIFs; the GIFs are embedded in the API binary (`go:embed`) and uploaded to Telegram once per bot (file_id cached). `tgOut` gets a `Scene` slug; `sendTGOut` sends `sendAnimation`. News posts get a `scene` column and an admin picker.

**Tech Stack:** Python 3 + Pillow 10 (local), Go 1.26 (`internal/telegram`, `cmd/api`), PostgreSQL migration, React 19 admin (`admin_web`).

**Spec:** `docs/superpowers/specs/2026-11-18-telegram-hedgehog-scenes-design.md`

## Global Constraints

- Slugs, exactly: `wave coin piggy gift alarm wallet sleep news support`.
- GIF: 640×360, frame 40 ms (25 fps), 50–80 frames, infinite loop, no text in the picture, ≤ 1 MB each, one hedgehog per picture, no speech bubbles.
- Colours (hiearth.css tokens): bg `#090C14`, raised `#0F141F`, body `#151B28`, outline `#EEF2F7`, spikes/accent `#22C55E`, sparks `#4ADE80`, drop/glass `#7AA7FA`, cheek `#F87171` at 45 %, line-strong `#2C3648`.
- Hedgehog geometry: the paths of `site/src/components/Hedgehog.jsx` (viewBox 132×96), stroke widths 3 (outline) / 4 (spikes).
- Caption limit 1024 characters: longer text goes as a plain message without the scene.
- Menu screens stay text; support: scene only on the ticket's first admin answer; cabinet feed and Happ never show scenes.
- Inside Telegram only Stars; never add site-payment links (Developer Terms 6.2).
- Public repo: no tokens, chat ids, addresses in code, tests or docs. Tests only on the owner's Telegram account; the first real channel post needs the owner's explicit «да» with the text shown.
- Russian copy, exactly: welcome caption `👋 Привет! Это HiEarth — защищённый доступ к интернету в одну кнопку.`; news errors `Выберите что-то одно: ёжик или своя картинка.` and `Неизвестная сценка.`; admin labels `Картинка`, `Ёжик`, `Своя картинка`, `Без картинки`.

## Review Focus

1. Bot token changed → cached scene file_ids belong to the old bot; the first send after the change must upload again (cache cleared with `tgPhotoIDs`). Test in Task 3.
2. A scene slug stored in an old outbox row or a news post that no longer exists in the binary → the message still goes out as text, never stuck in the outbox. Test in Task 3.
3. A news post with the default `news` scene and a body over 1024 characters → a clear validation error in admin, not a failed channel send. Test in Task 5.
4. A person presses `/start` with a `link_…` or `ref_…` parameter → no welcome scene for `link_`; `ref_` without an account gets it once before the menu. Test in Task 4.
5. A support ticket with several admin answers → only the first answer carries `support`. Test in Task 4.

---

### Task 1: Scenes art, spec and preview

**Files:**
- Create: `docs/telegram-brand/scenes/render.py`, `docs/telegram-brand/scenes/hedgehog.py`
- Create: `server/backend/cmd/api/tgscenes/{wave,coin,piggy,gift,alarm,wallet,sleep,news,support}.gif` (generated, committed)
- Modify: `docs/telegram-brand/index.html` (a «Сценки ежа» section showing the nine GIFs with where each goes)
- Modify: `site/design-system/hiearth-canon/spec/12-telegram.md` (new §12.6 «Сценки ежа»), `spec/02-components.md` §34.4 (one line: scenes live only in Telegram, see 12 §12.6)

**Interfaces:**
- Produces: the nine GIFs at `server/backend/cmd/api/tgscenes/<slug>.gif`; `python docs/telegram-brand/scenes/render.py [--check]`.

- [ ] **Step 1: Spec first** — §12.6: the scene table from the design (slug, picture, messages), the look and the rules (one hedgehog, no bubbles, no text, menu stays text, support first answer only, feed/Happ without scenes, ≤ 1024 caption else no scene). One line in 02 §34.4.
- [ ] **Step 2: `hedgehog.py`** — `sample_path(d: str, steps=24) -> list[list[tuple[float,float]]]` for the commands M L H V C Q A Z used in `Hedgehog.jsx`; `draw_hedgehog(img, x, y, scale, mood: str, t: float)` drawing body, spikes, eye (open / happy arc / sleep line), brow, cheek, smile, drop with breath (scaleY 1.025, 3.2 s) and blink (every 4.6 s); 4× supersampling, downscale LANCZOS.
- [ ] **Step 3: `render.py`** — one function per scene `scene_<slug>(t: float) -> Image` (t in 0..1 of the loop) and `main()` writing every GIF (`save_all`, `duration=40`, `loop=0`, `optimize=True`, palette shared across frames). Motion (the builder tunes poses):
  - `wave` paw arc beside the face swings ±25° twice; blink. `coin` two spring hops, a coin rises over the spikes flipping (x-scale = cos), sparks. `piggy` a coin falls into a piggy bank's slot, the bank bounces, happy eyes. `gift` box held in front, the lid hops, sparks. `alarm` alarm clock shakes ±8° in two bursts, brow up. `wallet` open empty wallet, worried brow, the drop sways. `sleep` eyes closed, slow breath, «z» float up (no other text). `news` newspaper held in front, eyes move left–right reading, the page lifts once. `support` headset over the head with a mic, sound arcs pulse by the mic, blink.
- [ ] **Step 4: `--check`** — asserts for every slug: file exists, size 640×360, 50 ≤ frames ≤ 80, every frame 40 ms, loop 0, file ≤ 1 048 576 bytes. Run `python -I docs/telegram-brand/scenes/render.py && python -I docs/telegram-brand/scenes/render.py --check` → `9 scenes OK`.
- [ ] **Step 5: Preview** — the «Сценки ежа» section in `docs/telegram-brand/index.html` (relative `../../server/backend/cmd/api/tgscenes/*.gif` copied next to it as `scenes/*.gif` for the artifact), republish the brand artifact https://claude.ai/artifact/BinziHmHM7Ao2T33HSTVTE.
- [ ] **Step 6: Commit** `feat(telegram): hedgehog scenes art and spec 12 §12.6`.
- [ ] **CHECKPOINT: show the owner the page and wait for his OK on the art before Task 2.** Changes he asks for loop back to Steps 3–5.

### Task 2: Telegram client — animations

**Files:**
- Modify: `server/backend/internal/telegram/types.go`, `server/backend/internal/telegram/bot.go`
- Test: `server/backend/internal/telegram/bot_test.go` (create if absent)

**Interfaces:**
- Produces: `type Animation struct{ FileID string \`json:"file_id"\`; FileUniqueID string \`json:"file_unique_id"\` }`; `Message.Animation *Animation \`json:"animation,omitempty"\``; `type SendAnimation struct{ ChatID any; Animation, Caption, ParseMode string; ReplyMarkup *InlineKeyboardMarkup; DisableNotification bool }` (json names as the Bot API); `func (b *Bot) SendAnimation(ctx, p SendAnimation) (*Message, error)`; `func (b *Bot) SendAnimationBytes(ctx context.Context, chatID, caption, parseMode string, kb *InlineKeyboardMarkup, silent bool, fileName string, data []byte) (*Message, error)`.

- [ ] **Step 1: Failing test** `TestSendAnimationBytes`: a fake `Doer` captures the request; asserts method `sendAnimation`, the multipart body has a file part named `animation` with filename `coin.gif`, fields `caption`, `parse_mode=HTML`, `disable_notification=true`; a JSON reply `{"message_id":7,"animation":{"file_id":"A1"}}` gives `m.Animation.FileID == "A1"`. `TestSendAnimationFileID`: JSON body has `"animation":"A1"`.
- [ ] **Step 2:** `go test ./internal/telegram/ -run Animation` → FAIL (undefined).
- [ ] **Step 3: Implement** — extract the body of `SendPhotoBytes` into `func (b *Bot) uploadFile(ctx context.Context, method, fileField string, fields map[string]string, fileName string, data []byte) (*Message, error)`; `SendPhotoBytes` and `SendAnimationBytes` call it.
- [ ] **Step 4:** `go test ./internal/telegram/` → PASS.
- [ ] **Step 5: Commit** `feat(telegram): sendAnimation in the Bot API client`.

### Task 3: Scene registry and the outbox

**Files:**
- Create: `server/backend/cmd/api/tgscenes.go`, `server/backend/cmd/api/tgscenes_test.go`
- Modify: `server/backend/cmd/api/tgbot.go:258-264` (`tgOut`), `tgbot.go` `sendTGOut`, `tgbot_admin.go:92,98` (clear the new cache too), `news.go` `registerNewsRoutes` (route)

**Interfaces:**
- Consumes: Task 2 `SendAnimation`, `SendAnimationBytes`, `Message.Animation`.
- Produces: `var tgScenes = []string{"wave","coin","piggy","gift","alarm","wallet","sleep","news","support"}`; `func isTGScene(slug string) bool`; `func tgSceneGIF(slug string) ([]byte, bool)` (from `//go:embed tgscenes/*.gif`); `var tgSceneIDs sync.Map` (slug → file_id); `tgOut.Scene string \`json:"scene,omitempty"\``; `GET /api/v1/tg/scenes/<slug>.gif` → `image/gif`, `Cache-Control: public, max-age=86400`, 404 for anything else.

- [ ] **Step 1: Failing tests** (`tgscenes_test.go`, no DB):
  - `TestTGScenesEmbedded`: every slug in `tgScenes` has non-empty bytes starting with `GIF89a`.
  - `TestSendTGOutScene`: fake bot answering `sendAnimation` with `{"message_id":1,"animation":{"file_id":"F-coin"}}`; first `sendTGOut(.., tgOut{Text:"x", Scene:"coin"})` → one `sendAnimation` multipart call; second → JSON call with `"animation":"F-coin"`; after `tgSceneIDs.Clear()` → multipart again (Review Focus 1).
  - `TestSendTGOutSceneFallbacks`: `Scene:"gone"` → `sendMessage` (Review Focus 2); text of 1025 runes with `Scene:"coin"` → `sendMessage`; `Photo` and `Scene` both set → `sendPhoto`; `Silent:true` → `disable_notification` in the animation call.
  - `TestTGSceneRoute`: `/api/v1/tg/scenes/coin.gif` 200 `image/gif`; `/api/v1/tg/scenes/../x.gif` and `/api/v1/tg/scenes/nope.gif` 404.
- [ ] **Step 2:** `go test ./cmd/api/ -run 'TGScene|SendTGOutScene'` → FAIL.
- [ ] **Step 3: Implement** `tgscenes.go` and the scene branch of `sendTGOut` (photo first; then a known scene within 1024 runes; an unknown slug logs `[tg-bot] сценка %q не найдена` and sends text). Add `tgSceneIDs.Clear()` beside both `tgPhotoIDs.Clear()`.
- [ ] **Step 4:** `go test ./cmd/api/ -run 'TGScene|SendTGOutScene'` → PASS; `go vet ./cmd/api/`.
- [ ] **Step 5: Commit** `feat(telegram): scenes in the outbox (embedded GIFs, file_id cache)`.

### Task 4: Scenes on the bot's messages

**Files:**
- Modify: `server/backend/cmd/api/tgbot_updates.go` (`reply` neighbour, `tgStart`, `tgLinkFromSite`), `tgbot_menu.go:466` (account created), `tgbot_menu.go:534` (signed in by ID), `stars.go:370`, `tgbot_notify.go` (`tgReminderMessage`, `queueTGExpired`, the today/renew-short notice, `notifyTGPurchase`, `notifyTGTopup`, `notifyTGReferral`), `support.go` `deliverSupportAnswer`, `internal/biz/support.go`
- Test: `tgbot_menu_pg_test.go`, `tgbot_pg_test.go`, `support_pg_test.go`, `stars_pg_test.go`

**Interfaces:**
- Consumes: Task 3 `sendTGOut`, `tgOut.Scene`.
- Produces: `func (c *tgCtx) replyScene(scene, text string, kb *tg.InlineKeyboardMarkup) (int, error)` (via `sendTGOut` to `tg.ChatIDString(c.chat)`, same blocked-chat handling as `reply`); `func biz.SupportIsFirstAnswer(ctx context.Context, ticketID int, msgID int64) (bool, error)` (no earlier `author='admin'` message in the ticket).

- [ ] **Step 1: Failing tests** (PG, `dbtest` runner on golang:1.26):
  - `TestTGChatMenu` (extend): account created and both «Telegram привязан» paths are `sendAnimation` calls containing the old texts; `/start` with no account sends `sendAnimation` with the welcome caption then the `sendMessage` menu; `/start link_<tok>` sends no animation; a second `/start ref_x` without an account sends the welcome again (Review Focus 4, the welcome is per `/start`, not stored).
  - `tgbot_pg_test.go`: queued `tg_outbox.payload` JSON has `"scene":"alarm"` (auto-renew off / renews fine), `"wallet"` (top-up needed and renew-short), `"sleep"` (expired), `"coin"` (purchase), `"piggy"` (topup), `"gift"` (both referral notices).
  - `support_pg_test.go`: two admin answers in one ticket → the first payload has `"scene":"support"`, the second none (Review Focus 5).
  - `stars_pg_test.go`: the paid reply is `sendAnimation` with «оплачена и действует».
- [ ] **Step 2:** run those tests → FAIL.
- [ ] **Step 3: Implement** — set `Scene` where the design table says; `tgStart`: when `c.user == nil` and the arg is not `link_…`, `replyScene("wave", <welcome caption>, nil)` before `tgShow(c, "home", 0)`.
- [ ] **Step 4:** `bash dbtest126.sh ./cmd/api/ ./internal/biz/` → PASS; `go test ./cmd/... ./internal/...` → PASS.
- [ ] **Step 5: Commit** `feat(telegram): hedgehog scenes on welcome, payments, reminders, support`.

### Task 5: News posts with a scene

**Files:**
- Create: `server/backend/migrations/000091_news_scene.up.sql` / `.down.sql` (`ALTER TABLE news_posts ADD COLUMN IF NOT EXISTS scene TEXT` / drop)
- Modify: `server/backend/internal/biz/news.go` (`NewsPost.Scene`, `NewsInput.Scene`, `newsCols`, insert/update — a sent post keeps its scene like `image_path`), `server/backend/cmd/api/news.go` (`validateNews`, `postNewsToChannel`, `queueNewsForBot`, `tgNews`, `updateNewsEverywhere`, `newsInputOf`)
- Test: `server/backend/cmd/api/news_pg_test.go`

**Interfaces:**
- Consumes: Task 3 `isTGScene`, `sendTGOut`, `tgOut.Scene`.
- Produces: JSON field `scene` on admin news items and inputs (`""` = none); `toNewsItem` (site feed) does not expose it.

- [ ] **Step 1: Failing tests:** `validateNews` with `Scene:"news"` and `Image` → `Выберите что-то одно: ёжик или своя картинка.`; `Scene:"nope"` → `Неизвестная сценка.`; `Scene:"news"` and a body making the caption > 1024 → the existing too-long-for-a-picture error (Review Focus 3); publishing a post with `scene=news` → the channel call is `sendAnimation`, the bot outbox payload has `"scene":"news"`, `/news` in the bot sends `sendAnimation`; editing a sent scene post uses `editMessageCaption`; the public feed JSON has no `scene` key.
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3: Implement** — one helper `newsOut(p *biz.NewsPost, kb) tgOut` (text + Photo or Scene) used by channel, bot queue and `/news`; every `p.Image != ""` check that means «has a picture» becomes `p.Image != "" || p.Scene != ""`.
- [ ] **Step 4:** PG tests PASS; `go build ./...`.
- [ ] **Step 5: Commit** `feat(news): a hedgehog scene as the post's picture`.

### Task 6: Admin «Новости» picker

**Files:**
- Modify: `admin_web/src/pages/NewsPage.jsx` (form `scene`, the «Картинка» block), `admin_web/src/utils/adminForms.js`
- Test: `admin_web/src/utils/adminForms.test.js`

**Interfaces:**
- Consumes: Task 5 `scene` field; Task 3 `/api/v1/tg/scenes/<slug>.gif`.
- Produces: `export const NEWS_SCENES = ['news','wave','coin','piggy','gift','alarm','wallet','sleep','support']`; `export function newsPicture(form) -> 'scene'|'image'|'none'`.

- [ ] **Step 1: Failing test:** `newsPicture({scene:'news',image:''}) === 'scene'`, `({scene:'',image:'a.png'}) === 'image'`, `({scene:'',image:''}) === 'none'`.
- [ ] **Step 2:** `cd admin_web && npm test` → FAIL.
- [ ] **Step 3: Implement** — `EMPTY.scene = 'news'`; the block: a three-way segmented control `Ёжик` / `Своя картинка` / `Без картинки`; `Ёжик` shows a row of 9 thumbnails (96×54, `rounded`, `border-default`, the selected one `border-accent`); switching clears the other field; payload sends `scene`; a sent post shows its picture read-only. Theme lock: tokens only, no shadows, Geist Mono for slugs.
- [ ] **Step 4:** `npm test` PASS, `npm run build` OK.
- [ ] **Step 5: Commit** `feat(admin): pick a hedgehog scene for a news post`.

### Task 7: Docs, deploy, prod check

**Files:**
- Modify: `docs/runbooks/telegram-bot.md` (scenes, BotFather «Edit Description Picture» with `wave.gif`), `CLAUDE.md` (migrations count, scenes line in the Telegram section)

- [ ] **Step 1:** Update the docs; commit `docs(telegram): hedgehog scenes`.
- [ ] **Step 2:** Merge the active branches (`refs/heads/security/hardening` and any newer), `go build ./... && go test ./cmd/... ./internal/...`, PG tests, `admin_web` `npm test && npm run build`.
- [ ] **Step 3:** Deploy the API (stop + `quick_deploy.go`) and the admin (`quick_admin_web.go`); `deploy --status`; `journalctl -u heyearth-api -n 30` shows migration 000091 applied and the bot poller started.
- [ ] **Step 4:** `curl -sI https://hiearth.ru/api/v1/tg/scenes/coin.gif` → `200`, `image/gif`.
- [ ] **Step 5:** On the owner's account only: queue one outbox row per scene to the owner's chat (kind `test`), see them delivered (`tg_outbox.status='sent'`), ask the owner how they look. Admin «Новости» screenshot of the picker (one, theme lock check).
- [ ] **Step 6:** Beads: close the scene tasks; memory `project_telegram_bot.md` updated.
