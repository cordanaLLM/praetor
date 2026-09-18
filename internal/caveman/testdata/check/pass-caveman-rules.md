## Operational Rules

1. **Act on verified state.** Read source files, run real commands before hypothesis or edit. Never guess flag names, library signatures, repo configuration from memory.

2. **Reuse before writing (HISS-19).** Before writing function, config loader, parser or command: grep repo for capability; extend or call what exists. Two implementations of one behavior = defect, not redundancy (they drift; second silently stops matching first).
   - Enforcement exists; do not build another checker. `praetorctl dedupe scan .` = function-level clones + utility sprawl; runs inside `make verify-all`.
   - Duplicate unavoidable -> state why in commit body.

3. **Lead with output.** Direct answers, diffs, commands. No filler preamble, no "Based on", no restatement, no chatter.

8. **No evasion.** Never attempt `--no-verify`, `LEFTHOOK=0`, or modifying `.git/hooks`. `cordana-standards[bot]` re-checks every pull request in ephemeral isolated sandbox.

push rejected x2: state stale (gate run wrote receipt after sync). fix: resync, push. no bypass.
