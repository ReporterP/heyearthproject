# Design — HiEarth site «Канон»

Scope: `site/` (marketing + user portal + B2B). Not `admin_web/` (Linear theme lock in CLAUDE.md) and not `flutter-app/`.

Direction (chosen 2026-11-16 through impeccable, seed `seed:24a8767be363fa2d`, canon card): the VPN category standard executed at Linear / Raycast / Arc craft, dark only, with four signature elements — connect orb, split-flap digits, номерок (HiEarth ID tag), ёжик (mascot).

- Source of truth for code: `site/design-system/hiearth-canon/hiearth.css` (tokens on `:root`, components and motion scoped under `.he`).
- Brand book, tokens and component cards: https://claude.ai/artifact/5GptLe8fU1HxtyavML6SSh
- Every screen and state: https://claude.ai/artifact/U6TEr9jSpR1cuRk9XcS5Li
- Implementation brief: `site/design-system/hiearth-canon/HANDOFF.md`
- **Binding behaviour spec** (every component state, show/hide rule, action, animation, string, and how to add new features): `site/design-system/hiearth-canon/spec/` — start at `spec/README.md`. New UI is specified there before it is coded.

## Tokens (summary)

- Surfaces: `--bg #090C14`, `--bg-raised #0F141F`, `--bg-elevated #151B28`, `--bg-hover #1B2232`, `--bg-press #222A3C`; lines `--line #1F2735`, `--line-strong #2C3648`.
- Text: `--fg #EEF2F7`, `--fg-muted #A1ABBD`, `--fg-subtle #8490A4`.
- Brand: `--accent #22C55E` (ink `#052E14`, text-on-dark `#4ADE80`). Signals: info `#3B82F6`/`#7AA7FA`, warning `#F5B544`, danger `#F87171`.
- Type: Manrope (UI + display, 800 for headings, tracking −0.035…−0.045em), JetBrains Mono for data only. Scale 72/56/40/28/20/16/19/14/13.
- Radius 6/8/12/16/20/28/pill. Spacing 4-px grid. Shadows: `--shadow-pop` (floating), `--shadow-lift` (selected). No coloured glows.
- Motion: 120/200/320/600 ms; ease-out for entrances; spring only for switches and the mascot; reduced motion respected.

## Rules

One green primary action per screen. Borders, not shadows, for cards. States never by colour alone. No eyebrows, no gradient text, no coloured left borders, no glass as decoration, no glows. The word «VPN» is never in copy.
