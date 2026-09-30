# Praetor VS Code extension

This extension connects a trusted workspace to the shared Praetor CLI. It offers
the workspace's `standards-mcp` server to VS Code chat through the MCP server
definition provider API. Its agent setup command reads the Go adapter inventory
and prepares or applies MCP client configuration for other clients with the same
preservation, conflict, backup and readback checks as `praetorctl clients`.

The complete goal is [IDE-driven agent setup and enforcement](../../docs/plans/ide-agent-setup.md).
Wrapper installation, native client activation, enforcement receipts and drift
repair remain open stages. The status bar reports **Unverified** until those
stages have real evidence. It shows only while a workspace folder root holds
`.standards.yaml` or `AGENTS.md`, the files the `workspaceContains` activation
events name (`PRAETOR_MARKERS` in `src/setup.ts`), so an activation from a Go
file or from MCP discovery in an unrelated workspace shows nothing.

## Build and test

From the repository root, with Go and Node/npm available:

```sh
make vscode-test
```

This installs the lockfile dependencies without install scripts, compiles strict
TypeScript, runs subprocess and setup tests, and builds a temporary Go CLI for a
real configuration prepare/apply test. `make verify-all` includes this gate.
The runtime dependency is `vscode-languageclient` 9.0.1; the declared minimum host
is VS Code 1.107 (see [Minimum host](#minimum-host)). These tests do not launch
an extension host. With an installed
VS Code and a working display, run `npm run test:host --prefix editors/vscode`
for an isolated host smoke test. It uses disposable user data, extension and
workspace directories, checks activation and command registration, and records
the observed trust state. When untrusted, it also verifies that the audit command
cannot execute a marker-writing CLI. Local VS Code 1.137.0 passed this smoke test.
Other editors, native agent sessions, marketplace publication and a packaged
VSIX remain unqualified.

## Use the setup command

Set the user/machine `standards.cli.path` to an installed `praetorctl` executable
or a command on the extension host's PATH. Workspace overrides are ignored for
this setting. In a trusted workspace, run **Standards: Prepare Agent MCP
Configuration** and select:

1. A client from the shared capability inventory and Prepare or Apply.
2. A private shared server registry and, where supported, an existing or new
   client configuration target.
3. An existing private artifact parent and a new directory name within it.

Preparation writes a review plan. Apply also preserves a backup before replacing
the exact selected target. Codex and AGY currently expose native command plans
and cannot use file Apply. Existing directory names, merge conflicts and changed
configuration are errors; inspect retained artifacts before retrying.

Commands select a workspace explicitly in a multi-root window, use separate
arguments without a shell, capture at most 1 MiB of output, and enforce bounded
timeouts and cancellation. Verification runs the selected workspace's
`make verify-all`; configure its timeout with `standards.verificationTimeoutSeconds`.
Cancellation attempts POSIX process-group termination or Windows child-process
termination. This is not a sandbox or proof that detached descendants stopped.

The optional LSP starts only for a trusted workspace, using its scoped
`standards.lsp.path`. With multiple roots it binds to the active editor's root;
it does not start against an arbitrary first folder. The default is
`${workspaceFolder}/bin/standards-lsp`. An absolute path that names no file
starts nothing and logs one line to the **Praetor** output channel; the
**Praetor LSP unavailable** warning is reserved for a binary that exists but
fails to start. The existence check is the one the MCP provider uses
(`commandAvailable` in `src/setup.ts`). Every existence probe, including the
status-bar marker check, is raced against a 1 s timer (`boundedIsFile`,
`FILE_PROBE_TIMEOUT_MS`), so a stalled network or remote mount counts as absent
instead of hanging activation. All paths and processes belong to the actual
extension host, which may be remote or in a container.

## MCP server

The extension contributes the MCP server definition provider `standards.mcp`
(`contributes.mcpServerDefinitionProviders` in `package.json`) and registers it
with `vscode.lm.registerMcpServerDefinitionProvider` on activation
(`src/extension.ts`). VS Code chat then lists one stdio server, **Praetor
standards-mcp**, started as `<path> -transport=stdio -root <folder>` with the
folder as its working directory. No `.vscode/mcp.json` entry is needed; adding
one for the same binary lists the server twice.

VS Code derives an `onMcpCollection:standards.mcp` activation event from the
contribution and registers the collection only while its `when` clause holds:
`isWorkspaceTrusted && config.standards.mcp.enabled && workspaceFolderCount > 0`.
MCP discovery therefore never activates the extension in an untrusted or empty
window, or with `standards.mcp.enabled` set to `false`.

| Setting | Default | Effect |
| :-- | :-- | :-- |
| `standards.mcp.enabled` | `true` | `false` offers no server |
| `standards.mcp.path` | `${workspaceFolder}/bin/standards-mcp` | server executable; blank falls back to the default |

Within that gate, the provider (`src/mcp.ts`) offers the server only when all
of these hold:

- the workspace is trusted. `standards.mcp.path` is a restricted setting, like
  `standards.lsp.path`;
- a folder is selected by the same rule as the LSP: the only folder, or the
  active editor's folder in a multi-root window. While the active editor belongs
  to no folder (Settings, a preview, a chat tab, every editor closed), the
  previous folder stays selected, so a focus change never withdraws a running
  server (`stickyFolder` in `src/mcp.ts`);
- an absolute path names an existing file. On Windows `<path>.com` and
  `<path>.exe` also count, so `make build` output `bin/standards-mcp.exe` serves
  the default. A command name or relative path is passed on unchecked.

The provider fires `onDidChangeMcpServerDefinitions`, and VS Code asks again,
when:

- a `standards.mcp.*` setting changes, Workspace Trust is granted or the folder
  set changes;
- in a multi-root window, the active editor moves to another folder, including
  the first editor opened after activation. VS Code then stops the server on the
  old root and starts it on the new one;
- one of the checked files above is created or deleted, for example when
  `make build` writes `bin/standards-mcp` after the window opened.
  `LaunchFileWatch` in `src/mcp.ts` keeps one non-recursive watcher per checked
  file, at most three, and replaces them when the path changes. A non-recursive
  watch still reports files that `files.watcherExclude` hides, such as the `bin`
  entry Praetor's generated Antigravity settings add, and a watch on a directory
  that does not exist yet starts once it is created.

The LSP and the MCP server resolve `${workspaceFolder}` through one helper,
`workspaceExecutable` in `src/setup.ts`. Tests: `src/mcp.test.ts` (provider)
and `src/setup.test.ts` (manifest contract).

An editor that satisfies `engines.vscode` but has no
`vscode.lm.registerMcpServerDefinitionProvider` keeps the commands and the LSP
and offers no server. `praetorctl clients` has no VS Code adapter, so it never
writes a second VS Code entry; its setup command serves the other clients.

## Minimum host

`engines.vscode` is `^1.107.0`, and the pinned `@types/vscode` is `1.107.0`,
because `vsce` refuses host types newer than the engine floor. The floor keeps
Antigravity IDE, which reports VS Code 1.107.0, and other VS Code-based editors
that trail upstream on the update path. Everything the extension uses is in
1.107.0:

- the MCP provider API (`lm.registerMcpServerDefinitionProvider`,
  `McpStdioServerDefinition`) first ships in 1.101.0, and its declarations in
  `@types/vscode` 1.107.0 and 1.125.0 are identical;
- the contribution `when` clause first ships in 1.105.0. It is
  microsoft/vscode#268097, the fix for microsoft/vscode#266221, where VS Code
  activated every MCP provider extension in empty workspaces. In 1.107.0,
  `extensionMcpDiscovery.ts` registers a collection that has a `when` only
  while the clause holds, and only that collection's lazy load fires
  `onMcpCollection:standards.mcp`.

VS Code and forks older than 1.107 no longer install updates of this extension.
`src/setup.test.ts` checks that the floor, the pinned types and the lockfile
agree, and that the floor lies between 1.105.0 (the `when` clause) and 1.107.0
(the trailing fork host). Renovate leaves `@types/vscode` alone (the
`editors/vscode/package.json` rule in `renovate.json`), so the pin moves only
when someone raises the floor with it.

## Settings

Every contributed setting has a reader in `src/extension.ts`, except
`standards.lsp.trace.server`, which vscode-languageclient reads itself.
`src/setup.test.ts` fails on a contributed setting nothing reads.

**Standards: Check Sentinel Host Headroom** runs
`praetorctl sentinel --min-free-mb=<standards.sentinel.headroomMB>` (default
1024 MiB) in a trusted workspace. The CLI measures free RAM
(`internal/sentinel`) and exits non-zero when it is below the headroom or when
the host has no memory reading, as on macOS. The legacy `standards.modelTier`
setting is no longer contributed, because nothing read it; delete it from
settings files. The Go generator's `IncludeMCP` option is retained for source
compatibility and writes nothing.

Inspect command output in the **Praetor** output channel. A configuration command
finishing successfully does not establish native trust, tool use, wrapper
execution, policy enforcement or model state.
