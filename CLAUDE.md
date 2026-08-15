# Project Instructions for AI Agents

This file provides instructions and context for AI coding agents working on this project.

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

Mirror of `AGENTS.md`. These override the managed Beads blocks above
(including any instruction to `bd close` or commit on `main`).

Cursor always-on copies: `.cursor/rules/orchestrator.mdc` and
`.cursor/rules/workers.mdc`.

### Roles

**Owner** — product decisions, deploy («заливай»), git push. Does **not**
accept each bead.

**Orchestrator** — plans, creates/claims beads, dispatches workers, **verifies
and accepts** (diff + tests + acceptance criteria), closes beads, asks a
weak model to commit and update CHANGELOG, then merges into `main`. Does
**not** wait for owner «принимаю». Does **not** implement production code,
does **not** commit, does **not** write CHANGELOG itself.

**Implementing worker** — one claimed bead, isolated worktree/branch
(superpowers: using-git-worktrees). Reports diff + test evidence. Does
**not** commit (unless asked), does **not** merge, does **not** `bd close`,
does **not** push or deploy.

**Weak model (`composer-2.5-fast`)** — after orchestrator accept: one commit
on the feature branch (why, not what); gitignored CHANGELOG `[Unreleased]`
in Russian with the task ID. Does not add CHANGELOG to git.

### Shared rules

- **Tasks live ONLY in beads.** Never create or maintain markdown task
  lists. `AUDITS/TASKS.md` is a frozen archive.
- **Do not change production code without a task (bead).**
- **One task = one commit on a feature branch, not on main.** Merge only
  after verification (superpowers: finishing-a-development-branch).
- Workers report to the orchestrator; they do **not** close their own bead.
- Only the orchestrator closes beads after acceptance criteria pass.
- New defect → new bead; do not fix without a task. Rework notes are binding.
- CHANGELOG.md is gitignored: [Unreleased], Russian, Keep a Changelog, task ID.
- Never commit `app/panel/internal/web/static/input.css` or secrets.
- No git push / VPS deploy unless the owner explicitly asks.

## Required skills and MCP

Canonical list: `AGENTS.md` (same heading). Always-on: `.cursor/rules/skills-and-mcp.mdc`.

**Skills (required):** `beads`; Superpowers `using-git-worktrees`,
`finishing-a-development-branch`, `verification-before-completion`,
`requesting-code-review` / `receiving-code-review`, `systematic-debugging`,
`test-driven-development`. Orchestrator: `subagent-driven-development`,
`dispatching-parallel-agents`, `writing-plans`.

**When it matches:** project `.agents/skills/tailwindcss` (panel CSS;
`input.css` frozen until UI bead), project `.agents/skills/cli-for-agents`
(install/bootstrap/panel CLI), `review-security` / `review-bugbot` before
merge.

**Plugins:** `.cursor/settings.json` enables `superpowers`, `playwright`,
and `cli-for-agent` (not Figma/GitHub).

**MCP:** project `.cursor/mcp.json` launches Playwright only
(`npx -y @playwright/mcp@latest`); also `cursor-ide-browser` for the
panel and `cursor-app-control` after worktrees. VPS is SSH, not MCP.

**Not required:** Figma, GitHub (no origin), Orca until installed, random
skills.sh Go/Docker/VPS packs.

## Build & Test

_Add your build and test commands here_

```bash
# Example:
# npm install
# npm test
```

## Architecture Overview

_Add a brief overview of your project architecture_

## Conventions & Patterns

_Add your project-specific conventions here_
