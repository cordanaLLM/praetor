# README presentation standard

The README presentation gate checks that an adopted repository's front page
(`README.md`) follows the shared presentation standard established in ADR-3366
and exemplified by VMAFx (order `engine-readme-presentation-check-20261022`,
node `DOC1`). The gate is `readmepresentation.Audit` in
`internal/readmepresentation/audit.go`, invoked by `praetorctl audit` and the
MCP `standards_audit` tool.

GitHub renders a README in a column approximately 830 pixels wide on desktop
browsers, and narrower on mobile viewports. The rules prevent mid-page badge
sprawl, oversized hero banners, unthemed diagrams, and disordered sections.

## Rules

| Rule ID | Subject | Requirement |
| :--- | :--- | :--- |
| `README-00` | Presence | `README.md` must be present at the repository root; absent file is refused. |
| `README-01` | Badge block | All `style=for-the-badge` badges sit in one contiguous block under the title or banner; none further down. Group comments `<!-- N name -->` must follow fixed rank order: (1) release/version, (2) CI/build, (3) security/scorecard, (4) standards, (5) licence, (6) support. Missing groups are permitted; reordering is refused. |
| `README-02` | Tagline | The first non-empty text line after the badge block must be a single bold line (`**...**`, `__...__`, `<b>...</b>`, or `<strong>...</strong>`). |
| `README-03` | Hero banner | Header banner image `height` attribute must be at most 120; `width="100%"` (or 100% width style) is refused. |
| `README-04` | Diagrams & figures | Every Mermaid code block must begin with a `%%{init:` directive configuring `'theme': 'base'` and `themeVariables`. Any local `.svg` figure below the header must have a `viewBox` width of at most 830 units; the hero banner is exempt. |
| `README-05` | Sections & governance | Duplicate `H2` headings are refused. The managed governance block (`<!-- praetor:readme-governance:start -->`) must remain in the final section. Present sections must follow keyword class order: get-started, architecture, guides, contribute, support, roadmap, upstream, governance. |

## Section keyword order

When present, section `H2` headings must follow this ordering sequence:

1. `get-started`: `install`, `installation`, `get started`,
   `getting started`, `quick start`, `quickstart`
2. `architecture`: `architecture`, `at a glance`
3. `guides`: `guide`, `guides`, `reference`
4. `contribute`: `contribute`, `contributing`, `contribution`,
   `contributions`
5. `support`: `support`
6. `roadmap`: `roadmap`, `roadmap and releases`
7. `upstream`: `license`, `licence`, `upstream`
8. `governance`: `governance`, `standards and governance`

## Ratchet and enforcement

Findings convert into `internal/baseline` infractions (`README-00` through
`README-05` on `README.md`, `readmepresentation.ToBaseline`) and evaluate
through `baseline.EvaluateRatchet`. When `.standards-baseline.json` holds
`README-*` entries, recorded findings pass, new findings above the baseline
fail the audit, and resolved entries report as stale via
`ratchet.StaleNotice()`.

For an adopted repository without `README-*` baseline entries or an explicit
enforcement switch, `readmepresentation.Audit` prints findings (including
`README-00` when `README.md` is absent) on `[REPORT]` lines (`report: measured,
not enforced`, matching `HISS-04` in `internal/hiss/measurement.go`) without
failing the audit exit code. To enforce the gate without baseline entries, set
`gate.readme_presentation` in `.standards.yaml`:

```yaml
gate:
  readme_presentation: true
```
