# Client schemas

Praetor writes configuration for coding clients and answers their hook and protocol
messages. Where a client publishes a JSON Schema for the format, Praetor vendors that
schema at an upstream release tag or commit, checks every rendered file and fixture
against it in tests, and generates Go types from it. A client release then arrives as a
Renovate pin bump whose failing test names the field that drifted.

## Pinned schemas

`internal/clientschema/vendor/manifest.json` is the one pin file. It records, per source,
the repository, the pin, the licence, the copyright holder and the sha256 of every file.
`internal/clientschema` reads it; no other code lists a schema.

| Client or format | Vendored file | Upstream | Pin | Licence |
| --- | --- | --- | --- | --- |
| Claude Code settings | `claude/claude-code-settings.json` | SchemaStore `src/schemas/json/claude-code-settings.json` | commit `ce64da2` | Apache-2.0 |
| Gemini CLI settings | `gemini/settings.schema.json` | `google-gemini/gemini-cli` `schemas/settings.schema.json` | `v0.63.0` | Apache-2.0 |
| Codex config | `codex/config.schema.json` | `openai/codex` `codex-rs/core/config.schema.json` | `rust-v0.162.0` | Apache-2.0 |
| Codex hook events | `codex/hooks/*.command.{input,output}.schema.json` (23 files) | `openai/codex` `codex-rs/hooks/schema/generated/` | `rust-v0.162.0` | Apache-2.0 |
| MCP protocol | `mcp/schema.json` | `modelcontextprotocol/modelcontextprotocol` `schema/2025-11-25/schema.json` | `2025-11-25` | MIT |
| opencode config | `opencode/config.json` | `https://opencode.ai/config.json` | hosted copy, release `v1.18.35` | MIT |

`clients capabilities` (and the MCP tool `standards_client_capabilities`) reports the pinned
version and sources per client in a `schema` member, read from the manifest
(`internal/clientsetup/capabilities.go`, `TestCapabilitiesReportThePinnedSchemaPerClient`).
A client without a vendored schema reports `state: none` and the reason.

Two pins are not release tags:

- **SchemaStore** has no release tags, so the pin is a commit of `master`.
- **opencode** commits no JSON Schema. The release tag holds the TypeScript source, and the
  published `config.json` is a generated file with no immutable reference. The manifest pins
  the sha256 of the hosted copy and names the release it was observed beside. The online check
  below fails when the hosted file moves, which is the signal to refresh.

The opencode schema references `https://models.dev/model-schema.json` for model entries.
Praetor renders none, so the tests replace that reference with an accept-all schema, stated
in `tools/schemacheck/render_test.go` (`opencodeExternal`). `Compile` refuses any external
reference without a stated replacement and never reaches the network.

Every vendored file keeps its upstream licence through `REUSE.toml` overrides (one table per
upstream, after the `**` table); `manifest.json` itself is EUPL-1.2.
`.gitattributes` stores the files byte for byte (`-text -whitespace`), because the digest
pins the exact bytes.

## What is checked

The validator is the nested, test-only module `tools/schemacheck`
(`github.com/santhosh-tekuri/jsonschema/v6`, Apache-2.0): the maintained Go implementation
that covers drafts 4 through 2020-12, including draft 7 (the Codex hook schemas) and draft
2020-12 (Gemini CLI, MCP, opencode). Praetor's production module has no JSON Schema
dependency. Run it with:

```bash
make client-schemas-test
```

| Check | Test |
| --- | --- |
| Every vendored file equals its pin; no vendored file is unpinned | `internal/clientschema` `TestVendoredFilesMatchTheirPins`, `TestNoUnpinnedFileInTheVendorDirectory` |
| Every pin is visible to Renovate | `TestRenovateTracksEveryPin` |
| Adoption hooks for Claude Code, Codex and Gemini CLI, the MCP registry for Gemini CLI and opencode, merges into existing settings, and this repository's tracked hook files validate | `tools/schemacheck` `TestRenderedClientConfigsValidateAgainstThePinnedSchemas` |
| A schema that renames a member Praetor renders fails and names it; the restored schema passes | `TestPlantedSchemaRenameFailsAndNamesTheField` |
| Each schema family refuses a mutated document | `TestEachSchemaRefusesAMutatedDocument`, `TestMCPSchemaDefinitionsRefuseMutatedMessages` |
| Codex hook payload fixtures validate against the published input schemas, and every registered Codex event has one | `TestCodexHookFixturesValidateAgainstThePublishedInputSchemas`, `TestEveryRegisteredCodexEventHasASchemaCheckedFixture` |
| The `standards-mcp` binary, started over stdio, answers `initialize`, `tools/list` and `tools/call` in the MCP shape | `TestStandardsMCPResponsesConformToTheMCPSchema` |

Offline runs need no network. The one check that fetches, comparing every vendored file with
its pinned upstream URL, runs only with `PRAETOR_CLIENT_SCHEMAS_ONLINE=1` and otherwise skips
with that reason (`TestVendoredSchemasEqualTheirUpstreamPins`).

### Limits the schemas set

- Gemini CLI's schema does not close the set of hook event names, so renaming an event there
  is not detected. Claude Code's and Codex's schemas are closed, and the planted-rename test
  covers them.
- Codex configuration is TOML. `codex mcp add` owns that file, and Praetor's `codex-mcp.toml`
  export is not validated, because the oracle reads JSON and YAML only. The Codex hooks file
  (`.codex/hooks.json`) is validated against the `hooks` member of the config schema.
- Codex hook outputs: Praetor's Codex dialect answers with an exit code and stderr, never a
  JSON document on stdout, so the output schemas are vendored and generated but no Praetor
  output is validated against them.

## Generated types

`go generate ./internal/clientschema` runs `internal/clientschema/typegen` and writes:

| File | Source | Content |
| --- | --- | --- |
| `internal/codexhook/events_gen.go` | the 23 Codex hook schemas | one input or output struct per event |
| `internal/mcpwire/messages_gen.go` | the MCP schema | the JSON-RPC envelopes, `initialize`, `tools/list`, `tools/call` messages and their closure |

The files are a declared generated artefact (`client schema types` in
`internal/generated/builtin.go`, [generated artefacts](generated-artefacts.md)). The staleness
gate is `TestGeneratedTypesAreFresh`: it fails, naming the file, when a committed file differs
from what the vendored schemas generate (`TestCheckFailsAStaleGeneration` plants the failure).
`PRAETOR_UPDATE_CLIENT_SCHEMA_TYPES=1 go test ./internal/clientschema/typegen` rewrites them.

`JSONRPCError` of `standards-mcp` and `ContentItem` of `internal/mcp` are aliases of the
generated `Error` and `TextContent`. The other hand-written MCP structs stay, each for a
stated reason:

- `ToolAnnotations` always encodes its four hints, including `false`; the generated type
  omits unset hints.
- `ToolResult` types its content as `[]ContentItem`; the schema's `ContentBlock` is a union.
- `JSONRPCRequest` and `JSONRPCResponse` carry an untyped `id` and a raw `params`.

`TestStandardsMCPResponsesConformToTheMCPSchema` holds their wire output to the schema
instead. `internal/agenthook` has no hand-written Codex payload struct: it decodes into maps,
so the generated Codex types serve the fixture tests (`codex_events_test.go`) and callers that
want a typed payload.

## Bumping a pin

1. Renovate changes `pin` in `manifest.json` (tag, hosted release or commit; `renovate.json`
   has the two custom managers and the version rules for the Codex and MCP tags).
2. The takeover refreshes the files:
   `PRAETOR_UPDATE_CLIENT_SCHEMAS=1 go test ./tools/schemacheck -run TestRefreshVendor`
   fetches every file at its pin and rewrites the files and their digests. It writes nothing
   if any fetch fails.
3. `go generate ./internal/clientschema` regenerates the types.
4. `make client-schemas-test` and `go test ./internal/clientschema/...` run. A removed or
   renamed member fails a rendering test naming the field; a changed hook payload fails a
   fixture test naming the missing field.

The MCP source URL contains the specification revision, so `{pin}` stands for the pin in
`url_base` and `upstream`, and a pin change moves every URL.

## Formats without an upstream schema

No JSON Schema was found for these, so they stay on documentation sources. Their rendering
tests assert Praetor's own contract; none of them is checked against an upstream document.

| Format | Written or read by | Documentation source |
| --- | --- | --- |
| Copilot CLI instructions (`.github/copilot-instructions.md`) | `compile-context` | GitHub Copilot custom instructions documentation |
| Antigravity (`agy`) `mcp_config.json`, plugin `hooks.json`, CLI permissions | `internal/clientsetup`, `internal/agenthook` | <https://antigravity.google/docs/plugins>, <https://antigravity.google/docs/permissions?tab=cli> |
| VS Code `mcp.json` | not written; the extension provides the server (`editors/vscode/README.md`) | VS Code MCP documentation |
| Claude Code `.mcp.json` | `internal/clientsetup` | <https://code.claude.com/docs/en/mcp> |
| Continue `.continue/mcpServers/praetor.yaml`, Cline and Kilo MCP exports | `internal/clientsetup` | <https://docs.continue.dev/customize/deep-dives/mcp>, <https://docs.cline.bot/mcp/mcp-overview>, <https://kilo.ai/docs/automate/mcp/using-in-kilo-code> |
| Cursor, Windsurf and Codex rule files | `compile-context` | the vendors' rules documentation; the files are Markdown |
| Claude Code, Gemini CLI and Antigravity hook payloads | `internal/agenthook` fixtures under `testdata/agent-text/claude`, `testdata/agent-text/gemini`, `testdata/agent-text/agy`, `testdata/agy/docs` and `testdata/pre-tool/cases.json` | <https://code.claude.com/docs/en/hooks>, the Gemini CLI source cited in `internal/agenthook/registrations.go`, <https://antigravity.google/docs/plugins> |
| Agent Skills | `compile-context` | the agentskills.io specification, prose only |

The Antigravity fixtures under `testdata/agy/docs` are copies of documentation examples. They
stay until the vendor publishes a schema; `TestUnvalidatedFixtureFamiliesAreListedInTheGuide`
fails when a new fixture family appears without a row here.

## Reusing the harness

`tools/schemacheck` is the shared harness of the formats epic (#910). Its surface is small:

- `Compile`, `CompileVendored` and `CompileDefinition` build a schema; `Validate` and
  `ValidateValue` return a `*Violations` that lists each failing field as a JSON pointer.
- `ReadYAML` reads YAML 1.2, so the workflow key `on:` stays a string and not a boolean
  (`TestReadYAMLKeepsOnAString`).
- `Fetch` and `SkipOffline` are the fetch-only path for a schema hosted in a copyleft
  repository: validate from the fetched copy and never commit it. Permissive schemas are
  vendored instead.
- `Refresh` rewrites a vendor directory from its manifest.

A new unit adds its schema to the manifest, a REUSE override with the upstream licence, a
rendering or fixture test and a negative case.
