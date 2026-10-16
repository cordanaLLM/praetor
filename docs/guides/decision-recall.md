# Decision recall

`praetorctl decisions` exports this repository's architectural decision records
as a pinned AsDecided corpus and serves that corpus over MCP stdio. The exporter
reads Markdown records from `docs/adr`, skips `README.md` and
`0000-template.md`, and writes `adr-<stem>.md` files below
`.standards/cache/decisions/decisions/adr/`. The managed `.gitignore` block
keeps this generated cache out of Git.

```bash
praetorctl decisions export --path .
praetorctl decisions pin-check
```

`export --out DIR` selects another output directory explicitly. Each export
rebuilds its `decisions/` subtree from the current ADR files. Conversion changes
the supported option headings to `Alternatives Considered`; a status line that
starts with `Proposed`, `Accepted`, `Superseded` or `Deprecated` and has extra
text becomes a bare status plus a `Status Note`. The remaining Markdown is
copied unchanged. `Transform` and `Export` are covered by
`internal/decisions/export_test.go`.

`pin-check` requires `decided-mcp` on `PATH` and checks version 0.31.2 against
the SHA-256 recorded in `internal/decisions/decisions.go`. The pin comes from
the [AsDecided v0.31.2 release](https://github.com/asdecided/core/releases/tag/v0.31.2);
the release checksum list was checked on 2026-10-16. A missing or mismatched
binary exits 1 and names the expected pin and release.
The supplied binary checksum is for Linux x86-64. Other operating systems and
architectures fail closed until their release binary hashes are independently
verified and added to the pin table.

```bash
praetorctl decisions serve --path .
```

`serve` exports the corpus, verifies the executable, and starts
`decided-mcp --root <output-directory>` with stdin, stdout and stderr connected
to the client. Adoption adds an `asdecided` project server to Claude's
`.mcp.json`, Gemini's `.gemini/settings.json`, OpenCode's `opencode.json`, and
Continue's `.continue/mcpServers/praetor.yaml` when at least one ADR record
exists. It preserves unrelated settings, leaves a conflicting user entry
untouched, and reports the conflict. Codex keeps its native CLI-managed config;
AGY's MCP file is host-local and Git-ignored, so both are reported as skips.
The merge and re-adoption cases are covered by
`internal/adoptclients/asdecided_test.go`.
