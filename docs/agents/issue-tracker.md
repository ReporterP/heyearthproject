# Issue tracker: Beads

Issues for this repo live in Beads (`bd`, Dolt server mode, issue prefix `heyearthproject-`). The Dolt database is local: it is not in git and has no remote.

Never use GitHub Issues or pull requests as the tracker: the GitHub repository is public.

## Conventions

- **Epic** for a large piece of work: `bd create -t epic "<title>"`. Its tickets are children: `bd create --parent <epic-id> …`.
- **Ticket:** `bd create --title="…" --description="…" --type=bug|feature|task --priority=0..4 --labels=<labels> --acceptance="…"`.
  - Priority 0 is critical.
  - QA severities P0–P3 map to priorities 0–3.
- **Blocking edge:** `bd dep add <blocked-id> <blocker-id>`. The frontier is `bd ready`.
- **Triage state:** exactly one label from `triage-labels.md` (`bd label add <id> <label>`, `bd label remove <id> <label>`).
- **Category:** the issue type, `bug` or `feature` (the skills' `enhancement`).
- **Comments:** `bd comments add <id> "…"`. Read an issue with `bd show <id>` and `bd comments <id>`.
- **Agent brief** (from `triage`) goes into `--design`.
- **Notes:** append through comments. `bd update --notes` replaces the notes.
- **Security findings** get the label `security` and are never copied into `docs/`.

## When a skill says "publish to the issue tracker"

Run `bd create` as above, as children of the epic when there is one. Create blockers first, so the edges can use real ids.

## When a skill says "fetch the relevant ticket"

`bd show <id>`, then `bd comments <id>`.
