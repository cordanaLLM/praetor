# Console: build brief

This brief is the work order for the session that builds the console in this folder. Read it whole before you start.

## What the console is

The console is the one screen the operator of a Praetor fleet reads: what needs them now, which decisions are open, what the agent sessions are doing, what the plan graph ranks next, and how much model quota is left. It is a static web app. It reads three JSON files that sit beside its `index.html` and does nothing else on the network.

## Inputs, and the one rule about data

- `console.schema.json` defines the three files: `cards.json` (operator cards), `graph.json` (status totals, quick wins, the ready frontier) and `status.json` (sessions, open decisions, holds). Each is a `$defs` entry; every file carries `version: 1` and `generated_at`.
- `fixtures/` holds synthetic examples of the three files, made by `fixtures/generate.py`. Use only these, or files you generate with that script (`--cards 500` and so on for size tests). Never type in, fetch or invent data that looks like a real fleet: no real repository, person, host or product names in fixtures, tests, screenshots or code comments.
- `tools/schemacheck/console_test.go` validates the fixtures against the schema. Keep it green; if you need a field the schema lacks, stop and say so in the pull request instead of changing the schema.

## Stack

- SvelteKit with Svelte 5 runes, `@sveltejs/adapter-static` (prerendered, no server), TypeScript, pnpm with a committed lockfile. Pin exact versions (checked 2026-10-19: `@sveltejs/kit` 3.1.0, `svelte` 5.57.2, `@sveltejs/adapter-static` 4.0.0).
- Components from `golusoris/sveltesentio` on npm, exact pins: `@sveltesentio/ui` 0.8.0 (tag `ui-v0.8.0`) for controls, `@sveltesentio/realtime` 0.7.0 for live refresh over server-sent events. Read their READMEs and use what they provide before you write a component of your own.
- Look and feel: the design tokens of `cordanaLLM/Aegis-OS` at `ui/concordia-tokens` (`concordia-tokens.css`: colour, type, spacing, the 3 px focus ring), and the visual language of its Forum shell described in that repository's `docs/adr/0004-native-p05-shell-on-gpui.md` (take the look, not the code: that shell is native). Both repositories are public; copy only what you need, keep its licence notice, add the source to `REUSE.toml` and to `docs/credits.yaml` with the upstream commit.
- Modern, calm, dark and light themes following the system setting with a manual switch; phone first, then wider layouts.
- No heavy chart library. Small inline SVG is enough.

## Tabs

1. **Now**: the operator cards, newest first. Each card shows its title and its one-line `say` (what the operator answers when it is done); it expands to `detail`, `why_now`, `consequence_if_skipped` and `est_minutes`. A card with `cmd` has a copy button that copies the command exactly; a card with `link` opens it. Group or filter by `state` (now, wait, later) and `where`.
2. **Decisions**: the open DECISION and ASK ids from `status.json` `decisions`, oldest first, with who sent them and when.
3. **Fleet**: the sessions from `status.json` `sessions` (repository, state, idle minutes, memory, latest line), with the holds in force.
4. **Graph**: `graph.json` totals as small bars, the quick wins (gain, effort, repository) and the ready frontier in rank order, with filters by class, goal, status and repository, and text search.
5. **Credit and quota**: data version 1 carries no quota file. Build the tab with an empty state that says which data version will carry it; do not invent a file format.
6. **History**: likewise empty until a later data version carries history.

## Behaviour that matters more than looks

- Interactive and never blocking: skeleton placeholders while a file loads, then stale-while-revalidate; never a full-page spinner.
- Live updates (`@sveltesentio/realtime`, and a timed refetch when no event stream is configured) never reset the open tab, the filters, the scroll position or which cards are expanded. Keep that view state per viewer (URL plus local storage, wrapped in try/catch so a blocked storage still works).
- A file that fails to load or fails the schema shows a clear inline error for that tab only, with the time of the last good data; the other tabs keep working.
- Keyboard: every action reachable without a mouse, visible focus, a command palette (Ctrl or Cmd + K) to jump to a tab, a card or a search.
- Text search across cards, decisions and graph items.

## Quality gates (CI, all in this pull request)

- **Accessibility: WCAG 2.2 AA and EN 301 549.** Playwright with `@axe-core/playwright` (pin `axe-core` 4.14.0) on every tab in both themes and at phone and desktop width, plus a keyboard-path test (tab order reaches every control, the palette opens and closes), contrast, and `prefers-reduced-motion` honoured. Commit one planted failing page (an image without alt text and a low-contrast button) under a test-only route and prove the job fails on it; the real pages must pass.
- **Bundle budget:** initial JavaScript at most 150 KB gzip, measured on the built output and enforced in CI; prove the check with a planted overrun (a test that imports a large module into a fixture build) that fails it.
- **Unit tests** for data loading, schema errors, view-state keeping across a live update, and the copy button.
- A GitHub Actions workflow `console.yml` that runs only when `console/**` changes: install with the lockfile, lint, type check, unit tests, build, bundle budget, accessibility. Pin actions by commit SHA like the other workflows here. It must not be a required check for changes outside `console/`.
- Follow the repository's own rules in `AGENTS.md` (conventional commits, `git commit -s`, no generated-by footer, REUSE headers).

## Deliverable

One pull request against `main` that adds the app under `console/` (source, tests, workflow, lockfile, README for the folder with build and preview commands). Put screenshots of each tab, rendered from the fixtures, in the pull request body (light and dark, phone and desktop). State the measured initial bundle size and the accessibility results. Do not deploy anything; the image is built and deployed elsewhere after review.
