# Praetor VS Code extension

This extension connects a trusted workspace to the shared Praetor CLI. Its agent
setup command reads the Go adapter inventory and prepares or applies MCP client
configuration with the same preservation, conflict, backup and readback checks
as `praetorctl clients`.

The complete goal is [IDE-driven agent setup and enforcement](../../docs/plans/ide-agent-setup.md).
Wrapper installation, native client activation, enforcement receipts and drift
repair remain open stages. The status bar reports **Unverified** until those
stages have real evidence.

## Build and test

From the repository root, with Go and Node/npm available:

```sh
make vscode-test
```

This installs the lockfile dependencies without install scripts, compiles strict
TypeScript, runs subprocess and setup tests, and builds a temporary Go CLI for a
real configuration prepare/apply test. `make verify-all` includes this gate.
The runtime dependency is `vscode-languageclient` 9.0.1; the declared minimum host
is VS Code 1.90. These tests do not launch an extension host. With an installed
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
`${workspaceFolder}/bin/praetor-lsp`. All paths and processes belong to the
actual extension host, which may be remote or in a container.

Legacy `standards.mcp.enabled`, `standards.mcp.path` and `standards.modelTier`
settings remain declared for compatibility but do not activate services. Editor
configuration generation no longer emits them. The Go generator's legacy
`IncludeMCP` option is retained for source compatibility; use the client setup
pipeline to produce real MCP configuration.

Inspect command output in the **Praetor** output channel. A configuration command
finishing successfully does not establish native trust, tool use, wrapper
execution, policy enforcement or measured model/headroom state.
