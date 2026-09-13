# VS Code agent bootstrap and safe Praetor CLI execution

**Recommendation (13 September 2026):** keep the extension as a thin, trusted
workspace adapter and invoke a pinned `praetorctl` binary through
`vscode.ProcessExecution` with an argument array and an explicit selected
workspace folder. Do not send interpolated strings to a terminal. Gate commands
that read or execute workspace content on Workspace Trust, and return a clear
restricted-mode result. Make the executable path a machine-scoped setting or a
validated user choice; never accept a workspace setting as an executable
authority while untrusted.

The baseline scaffold at commit `54435ec` in `editors/vscode/src/extension.ts`
started an LSP with a configurable executable and first workspace folder
(`:20-30`), but its audit,
compile, and GC commands call `terminal.sendText` with shell command strings
(`:100-143`). Its status bar claims `HISS: 100% Pass` and “verified” sentinel
headroom without invoking Praetor (`:60-95`, `:125-134`). The manifest exposes
workspace-overridable configuration by default and has no explicit
`capabilities.untrustedWorkspaces` declaration (`editors/vscode/package.json:45-86`).
The package has only TypeScript, Node types, VS Code types, and
`vscode-languageclient` development dependencies; no process runner is already
available.

## Relevant VS Code contracts

The [Workspace Trust extension guide](https://code.visualstudio.com/api/extension-guides/workspace-trust)
(current documentation checked 2026-09-13) supports manifest declarations
`untrustedWorkspaces: {supported: true|false|'limited', ...}`. A limited
extension can gate sensitive paths with `vscode.workspace.isTrusted` and
`onDidGrantWorkspaceTrust`; the `isWorkspaceTrusted` context key can hide or
disable commands. VS Code’s [Workspace Trust guide](https://code.visualstudio.com/docs/editing/workspaces/workspace-trust)
explains that Restricted Mode limits terminal, tasks, debugging, agents, and
extensions, but also warns that a malicious extension can ignore the boundary.
Trust is therefore a user consent boundary, not a sandbox or proof that another
extension is safe. The extension cannot universally force other extensions or
native clients to request permissions; their manifests, user settings, and
host policy control those behaviors.

For direct execution, the [VS Code API reference](https://code.visualstudio.com/api/references/vscode-api)
defines `ProcessExecution(process, options)` as an external process without
shell interaction; `options.cwd` sets its working directory. Use
`new vscode.Task(..., new vscode.ProcessExecution(binary, args, {cwd}))` or a
child-process wrapper with equivalent argv semantics. The API also defines
`ShellExecution`, but the [task-provider guide](https://code.visualstudio.com/api/extension-guides/task-provider)
warns that shell execution interprets quoting, expansion, and wildcards; it is
not the safe default for user or workspace data. Validate that the configured
binary is an absolute regular executable, resolve the chosen workspace folder,
and pass request/store paths as separate arguments. Capture exit status and
bounded output, and surface cancellation and timeout as incomplete results.

`vscode.workspace.workspaceFolders` is an array for multi-root workspaces.
`workspace.getWorkspaceFolder(uri)` maps a resource to its containing folder,
and
[`showWorkspaceFolderPick`](https://code.visualstudio.com/api/references/vscode-api)
returns a user-selected folder or `undefined` when no folder is open. Commands
must not silently use index zero when multiple roots exist. A picker or an
explicit command argument should select the root; no-root windows should return
an actionable “open a workspace” result.

Contributed commands and settings are declared in the extension manifest and
read with `workspace.getConfiguration`, as documented by the [contribution
points reference](https://code.visualstudio.com/api/references/contribution-points)
and [command guide](https://code.visualstudio.com/api/extension-guides/command).
Mark executable and trust-sensitive settings with the appropriate scope and
`restrictedConfigurations`; a workspace can otherwise provide an executable
path that changes what the extension runs.

## Small adapter seam and tests

The adapter should expose one pure request object: selected folder URI,
validated executable, fixed subcommand, argv, bounded deadline, and output
limit. A resolver performs workspace selection, trust/configuration checks, and
path validation. An executor receives argv and cwd, invokes `ProcessExecution`
or an equivalent no-shell child-process wrapper, and returns `{exitCode, stdout, stderr, cancelled, timedOut, outputExceeded}`. It must not
construct a shell command. Existing commands map to `praetorctl audit` and
`praetorctl compile-context` with the selected root as the process working
directory. Verification uses `make verify-all`; it is not a CLI subcommand.

Test the resolver and executor as ordinary Node/TypeScript unit tests with
fake process and workspace objects: trusted/untrusted, no-root, single-root,
multi-root selection, relative or symlinked executable, spaces and shell
metacharacters in paths, cancellation, timeout, non-zero exit, bounded output,
and configuration changes. Add an extension-host smoke test only for command
registration and trust gating. VS Code’s [extension testing guide](https://code.visualstudio.com/api/working-with-extensions/testing-extension)
explicitly notes that tests cannot programmatically grant or revoke Workspace
Trust, so trust scenarios need separate launched fixtures or manual acceptance.
The CLI itself should be tested independently against temporary repositories;
those tests provide stronger evidence than terminal screenshot or GUI tests.

The later local adapter revision has unit coverage for argv separation, invalid
executable values, bounded output, and timeout termination; `npm test` compiles
the package and passes the current 17 runner/setup tests, including real Go CLI
configuration preparation, application and backup readback. Those are package checks,
not a Workspace Trust transition test or native client activation proof.
The separate `test:host` command passed on installed VS Code 1.137.0: the extension
activated, registered five commands, and blocked an audit subprocess in the
observed untrusted workspace. It uses temporary profiles and the documented
extension-development/test CLI flags. This does not qualify other editors or
trusted multi-root command flows. The baseline scaffold findings above should be read against
commit `54435ec`; subsequent implementation work is not retroactive evidence for
that snapshot. The smoke test is partial host evidence; no native agent session
has proved full adapter behavior or granted permissions through this test.
