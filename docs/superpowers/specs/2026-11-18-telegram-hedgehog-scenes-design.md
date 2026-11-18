# Hedgehog scenes for the Telegram bot and channel — design

Date: 2026-11-18 · Owner decisions in chat on 18.11 · Epic: Telegram users' bot (heyearthproject-e2ra)

## Intent

The owner: posts, news and the bot's messages lack nice pictures with the hedgehog; he wants a fun
animated hedgehog for each message in the bot and for the channel. The goal is a bot and a channel that
feel alive and recognisably HiEarth, using the same hedgehog as the site.

## Goals

- Nine short animated scenes (2–3 s loop, no sound) with the site's hedgehog plus a prop each.
- The bot sends them with the event messages listed below (fresh messages, not menu edits).
- A news post (channel + bot) can carry a scene picked in admin «Новости» instead of an uploaded picture.
- The scenes enter the design system next to the five moods (spec 12, with a pointer from 02 §34.4).
- The «привет» scene doubles as the bot's BotFather description picture (the owner sets it).

## Non-goals

- The chat menu (Баланс, Подписка, Подключение, Друзья, Оплата, Поддержка, Новости, Настройки) stays text:
  it edits one message in place on every tap.
- The support dialog: only the first answer of a ticket gets the scene, not every reply.
- The cabinet news feed on the site and the Happ line show no scenes (one hedgehog per screen, 02 §34.4).
- The Mini App, the site's hedgehog moods, stickers, sound.

## Anti-goals (a failure even if it works)

- Off-brand art: pixel art, stock, a different character from scene to scene, colours outside «Канон».
- More than one hedgehog in a picture; the hedgehog speaking in bubbles (02 §34.4).
- An animation on every menu tap; heavy files that make the chat slow to load.
- Anything that hints at paying outside Telegram Stars inside the bot (Developer Terms 6.2 rule stands).

## Scenes and where they go (owner-approved list)

| Slug | Scene | Messages |
|---|---|---|
| `wave` | waves a paw | first `/start` of a person without an account (before the menu), «Аккаунт создан», «Telegram привязан» (sign-in by ID in the chat and the site link `/start link_…`) |
| `coin` | jumps with a coin, sparks | subscription bought / renewed (`notifyTGPurchase`), Stars payment done (`stars.go`) |
| `piggy` | drops a coin into a piggy bank | wallet topped up (`notifyTGTopup`) |
| `gift` | holds a gift | friend bonus (`notifyTGReferral`) |
| `alarm` | with an alarm clock | reminders: 3 days left / last day (`ReminderEnds`, `ReminderRenewsFine`) |
| `wallet` | worried over an empty wallet | not enough for auto-renew (`ReminderTopUp`, `notifyTGRenewShort`) |
| `sleep` | asleep | subscription ended (`queueTGExpired`) |
| `news` | with a newspaper | default scene offered for a news post |
| `support` | with a headset | the first support answer of a ticket |

## Look (owner-approved)

The site's hedgehog drawing (`site/src/components/Hedgehog.jsx`, viewBox 132×96) on the site background
`--bg #090C14`; body `--bg-elevated`, outline `--fg`, spikes `--accent #22C55E`, sparks `--accent-fg`,
drops/glass `--info-fg`, cheek `--danger-fg` at 45 %. Wide 16:9 frame, 2–3 s loop, no sound, no text in
the picture (the caption carries the meaning).

## Approaches

1. **Frames drawn by a script from the site hedgehog's geometry (picked).** A Python + Pillow script
   (the same way `docs/telegram-brand` avatar/cover were made) samples the SVG paths, adds the props,
   animates by keyframes and writes GIFs. Exact brand look, reproducible, small files (flat palette),
   no new tools to install. Cost: each prop and its motion is authored by hand in code.
2. **AI image/animation generators** (the pixel-art tools available here). Dropped: pixel style is not
   «Канон», the character drifts between scenes, credits per try.
3. **SVG + CSS like the site, captured in a browser to GIF.** Would reuse the site's CSS animations
   1:1, but needs a headless browser or SVG rasteriser that is not installed (a download) and a frame
   capture pipeline; the props would still be new SVG. More moving parts for the same result.

## How it is built (builder's calls, marked)

- **Art** — `docs/telegram-brand/scenes/render.py` renders `server/backend/cmd/api/tgscenes/<slug>.gif`
  (640×360, 25 fps, 60–75 frames, infinite loop; target ≤ 1 MB each; *my call*) and a preview page
  `docs/telegram-brand/scenes.html`. The GIFs are committed (brand art, no secrets) and embedded in the
  API binary with `go:embed`, so deploys need no extra files.
- **Telegram client** — `internal/telegram`: `SendAnimation` (file_id) and `SendAnimationBytes`
  (multipart upload), mirroring `SendPhoto`/`SendPhotoBytes`; `Message.Animation.FileID` read back.
- **Outbox** — `tgOut` gains `Scene string \`json:"scene,omitempty"\`` (old rows stay valid).
  `sendTGOut`: a photo wins; else a scene → `sendAnimation` with the cached file_id, or the embedded bytes
  on the first send per bot (`tgSceneIDs sync.Map`, cleared with `tgPhotoIDs` when the bot token changes).
  Text longer than the 1024-character caption limit → sent as plain text without the scene (*my call*).
  An unknown slug → plain text and a log line.
- **Direct replies** — a `c.replyScene(slug, text, kb)` helper next to `c.reply` for `wave` (welcome
  before the menu, account created, linked) and `coin` (Stars). The menu message itself is unchanged.
- **Notifications** — the functions in the table set `Scene` on the `tgOut` they queue. Support: the
  answer notice carries `support` only when it is the ticket's first admin message.
- **News** — migration `000091_news_scene`: `news_posts.scene TEXT NULL` (check: a known slug; never
  together with `image`). The API validates the slug; channel and bot sends use the animation with the
  post text as caption (the existing 1024 limit for posts with a picture applies). Feed/Happ ignore it.
  A public `GET /api/v1/tg/scenes/<slug>.gif` serves the embedded GIFs for the admin gallery (*my call*).
- **Admin «Новости»** — the «Картинка» block offers «Ёжик» (a row of scene thumbnails, `news`
  preselected) or «Своя картинка» (the current upload); choosing one clears the other. Admin theme lock
  applies (tokens, hairline borders, no shadows).
- **Spec** — `site/design-system/hiearth-canon/spec/12-telegram.md`: a section «Сценки ежа» (the table
  above, look, rules: one hedgehog, no bubbles, no text, menu stays text, feed without scenes);
  02 §34.4 gets one line pointing there (scenes live only in Telegram). Runbook `telegram-bot.md`: the
  BotFather description picture step.
- **Tests** — Go: `sendTGOut` with a scene (first upload multipart, then file_id; caption too long →
  text), each notifier sets its slug, news validation (slug, not with image) and channel send with a
  scene, the first-answer rule for support. Admin: `npm test` for any new pure helper; `npm run build`.
- **Rollout** — merge the active branches before deploying (security/hardening first); migrations run
  at API start. The first real channel post with a scene still needs the owner's explicit «да» with the
  text shown. The owner sets the BotFather description picture himself.

## Left to the builder

Exact poses and motion of each scene, prop shapes, frame counts within the limits above, file sizes,
thumbnail size in the admin gallery.
