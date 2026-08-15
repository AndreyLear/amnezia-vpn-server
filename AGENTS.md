# Agent Instructions

This project uses **bd** (beads) for issue tracking. Run `bd prime` for full workflow context.

> **Architecture in one line:** Issues live in a local Dolt database
> (`.beads/dolt/`); cross-machine sync uses `bd dolt push/pull` (a
> git-compatible protocol), stored under `refs/dolt/data` on your git
> remote — separate from `refs/heads/*` where your code lives.
> `.beads/issues.jsonl` is a passive export, not the wire protocol.
>
> See [sync-concepts](https://github.com/gastownhall/beads/blob/main/docs/core-concepts/sync-concepts.md)
> for the one-screen overview and anti-patterns (don't treat JSONL as the
> source of truth; don't `bd import` during normal operation; don't
> reach for third-party Dolt hosting before trying the default).

## Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work atomically
bd close <id>         # Complete work
bd dolt push          # Push beads data to remote
```

## Non-Interactive Shell Commands

**ALWAYS use non-interactive flags** with file operations to avoid hanging on confirmation prompts.

Shell commands like `cp`, `mv`, and `rm` may be aliased to include `-i` (interactive) mode on some systems, causing the agent to hang indefinitely waiting for y/n input.

**Use these forms instead:**
```bash
# Force overwrite without prompting
cp -f source dest           # NOT: cp source dest
mv -f source dest           # NOT: mv source dest
rm -f file                  # NOT: rm file

# For recursive operations
rm -rf directory            # NOT: rm -r directory
cp -rf source dest          # NOT: cp -r source dest
```

**Other commands that may prompt:**
- `scp` - use `-o BatchMode=yes` for non-interactive
- `ssh` - use `-o BatchMode=yes` to fail instead of prompting
- `apt-get` - use `-y` flag
- `brew` - use `HOMEBREW_NO_AUTO_UPDATE=1` env var

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:1105d646 -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

**Architecture in one line:** issues live in a local Dolt DB; sync uses `refs/dolt/data` on your git remote; `.beads/issues.jsonl` is a passive export. See https://github.com/gastownhall/beads/blob/main/docs/core-concepts/sync-concepts.md for details and anti-patterns.

## Agent Context Profiles

The managed Beads block is task-tracking guidance, not permission to override repository, user, or orchestrator instructions.

- **Conservative (default)**: Use `bd` for task tracking. Do not run git commits, git pushes, or Dolt remote sync unless explicitly asked. At handoff, report changed files, validation, and suggested next commands.
- **Minimal**: Keep tool instruction files as pointers to `bd prime`; use the same conservative git policy unless active instructions say otherwise.
- **Team-maintainer**: Only when the repository explicitly opts in, agents may close beads, run quality gates, commit, and push as part of session close. A current "do not commit" or "do not push" instruction still wins.

## Session Completion

This protocol applies when ending a Beads implementation workflow. It is subordinate to explicit user, repository, and orchestrator instructions.

1. **File issues for remaining work** - Create beads for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **Handle git/sync by active profile**:
   ```bash
   # Conservative/minimal/default: report status and proposed commands; wait for approval.
   git status

   # Team-maintainer opt-in only, unless current instructions forbid it:
   git pull --rebase
   git push
   git status
   ```
5. **Hand off** - Summarize changes, validation, issue status, and any blocked sync/commit/push step

**Critical rules:**
- Explicit user or orchestrator instructions override this Beads block.
- Do not commit or push without clear authority from the active profile or the current user request.
- If a required sync or push is blocked, stop and report the exact command and error.
<!-- END BEADS INTEGRATION -->

## Execution Rules (owner, 2026-08-14)

These rules apply to ALL agents working in this repository. They override
anything contradictory in the managed blocks above (including beads.mdc
telling workers to `bd close` or commit on the current branch).

Cursor always-on copies: `.cursor/rules/orchestrator.mdc` and
`.cursor/rules/workers.mdc`.

### Roles

**Owner** — product decisions, acceptance, deploy («заливай»), git push.

**Orchestrator** — plans, creates/claims beads, dispatches workers, verifies
tests and acceptance, closes beads, asks a weak model to commit and update
CHANGELOG, then merges the feature branch into `main`. Does **not**
implement production code, does **not** commit, does **not** write
CHANGELOG itself.

**Implementing worker** — one claimed bead, isolated worktree/branch
(superpowers: using-git-worktrees). Reports diff + test evidence. Does
**not** commit (unless asked), does **not** merge, does **not** `bd close`,
does **not** push or deploy.

**Weak model (`composer-2.5-fast`)** — after owner accept: one commit on
the feature branch (why, not what); gitignored CHANGELOG `[Unreleased]`
in Russian with the task ID. Does not add CHANGELOG to git.

### Shared rules

- **Tasks live ONLY in beads.** Never create or maintain markdown task
  lists (no TODO files, no tasks.md). The historical `AUDITS/TASKS.md` is
  a frozen archive — do not update it, do not read it for work.
- **Do not change production code without a task (bead).**
- **One task = one commit on a feature branch, not on main.** After the
  orchestrator verifies tests and the owner accepts, merge into main
  (superpowers: finishing-a-development-branch). Do not land unverified
  work on main.
- **Commits and CHANGELOG.md** are written by a small/fast model dispatched
  by the orchestrator, not by the implementing worker unless asked.
- **After completing work, report to the orchestrator:** diff, test
  output, evidence. Do NOT close your own bead.
- **Only the orchestrator closes beads**, after verifying the acceptance
  criteria. If your work is returned "на доработку" (rework), the review
  notes are binding — fix them and report again. Re-closing without
  addressing the notes is a process violation.
- **Found a new defect?** Stop, report it — do not fix without a task.
- **Accepted changes are recorded in CHANGELOG.md** (local file,
  intentionally gitignored — do not add it to git). Keep a Changelog,
  [Unreleased], Russian, one line per change with the task ID.
- Never commit `app/panel/internal/web/static/input.css` or secrets.
- No git push / VPS deploy unless the owner explicitly asks.

## Required skills and MCP

This stack is a Go panel + SQLite + Docker Compose + AmneziaWG, with
server-rendered HTML and Tailwind v4 assets. Do not load Figma/React/Next
skills for ordinary panel work.

Cursor always-on pointer: `.cursor/rules/skills-and-mcp.mdc`.

### Skills — required (every implementation task)

Already installed (project or Cursor Superpowers plugin). Invoke the
matching skill before acting.

| Skill | Where | Who |
|---|---|---|
| `beads` | `.agents/skills/beads/` (project) | all |
| `using-git-worktrees` | Superpowers | worker |
| `finishing-a-development-branch` | Superpowers | orchestrator after accept |
| `verification-before-completion` | Superpowers | worker + orchestrator |
| `requesting-code-review` | Superpowers | orchestrator before merge |
| `receiving-code-review` | Superpowers | worker on rework |
| `systematic-debugging` | Superpowers | worker on failures |
| `test-driven-development` | Superpowers | worker on features/fixes |

### Skills — orchestrator

| Skill | Where |
|---|---|
| `using-superpowers` | Superpowers (session start) |
| `subagent-driven-development` | Superpowers |
| `dispatching-parallel-agents` | Superpowers (independent tasks) |
| `writing-plans` / `executing-plans` | Superpowers (multi-step) |
| `brainstorming` | Superpowers (new product behavior) |

### Skills — when the task matches

| Skill | When | Where |
|---|---|---|
| `tailwindcss` | Panel HTML/CSS. Do not edit `input.css` until the UI redesign bead is active. | `.agents/skills/tailwindcss/` (project) |
| `cli-for-agents` | `install.sh`, `bootstrap.sh`, `panel` CLI flags/help. | `.agents/skills/cli-for-agents/` (project) |
| `review-security` | Auth, TOTP, backups, nftables, exposed ports. | Cursor plugin |
| `review-bugbot` | Before merge, if the owner asks for a Bugbot-style pass. | Cursor plugin |

Project Cursor plugins (`.cursor/settings.json`): `superpowers`, `playwright`, `cli-for-agent`. Do not enable Figma or GitHub.

### MCP — use

| Server | Why |
|---|---|
| Playwright (`playwright`) | Project `.cursor/mcp.json` (`npx -y @playwright/mcp@latest`). Headless checks of the panel (login, backups, clients). |
| `cursor-ide-browser` | Visual check of `https://panel.super-space.com.de` after deploy. |
| `cursor-app-control` | Switch workspace root after creating a worktree. |

VPS access is **SSH** (`BatchMode=yes`, key auth), not an MCP. Deploy
recipe lives in operator notes: rsync `app/panel/` (exclude node_modules),
`compose.yaml`, `versions.lock` → `/opt/amnezia-vpn`; rebuild `panel` only
when the owner says «заливай».

### MCP / skills — not required

- **Figma** (`plugin-figma-figma`, `user-figma-console`) and Figma skills —
  UI redesign is deferred; do not invent a Figma workflow for panel bugs.
- **GitHub** (`plugin-github-github`) — no git remote; no PRs until origin
  exists.
- **Orca** (`orca-cli`, `orchestration`) — optional. `orca` currently
  points at a missing Orca.app. Use Cursor Task until the owner installs
  Orca.
- Random skills.sh Go/Docker/VPS packs (`golang-pro`, `docker-vps-deploy`,
  etc.). This repo already has `compose.yaml`, `install.sh`, `bootstrap.sh`,
  and `test_m*` harnesses.

<!-- BEGIN BEADS CODEX SETUP: generated by bd setup codex -->
## Beads Issue Tracker

Use Beads (`bd`) for durable task tracking in repositories that include it. Use the `beads` skill at `.agents/skills/beads/SKILL.md` (project install) or `~/.agents/skills/beads/SKILL.md` (global install) for Beads workflow guidance, then use the `bd` CLI for issue operations.

### Quick Reference

```bash
bd ready                # Find available work
bd show <id>            # View issue details
bd update <id> --claim  # Claim work
bd close <id>           # Complete work
bd prime                # Refresh Beads context
```

### Rules

- Use `bd` for all task tracking; do not create markdown TODO lists.
- Run `bd prime` when Beads context is missing or stale. Codex 0.129.0+ can load Beads context automatically through native hooks; use `/hooks` to inspect or toggle them.
- Keep persistent project memory in Beads via `bd remember`; do not create ad hoc memory files.

**Architecture in one line:** issues live in a local Dolt DB; sync uses `refs/dolt/data` on your git remote; `.beads/issues.jsonl` is a passive export. See https://github.com/gastownhall/beads/blob/main/docs/core-concepts/sync-concepts.md for details and anti-patterns.
<!-- END BEADS CODEX SETUP -->
