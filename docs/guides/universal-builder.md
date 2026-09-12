# Universal builder execution status

`praetorctl build` reads `.framework-build.yaml` but currently has no executable
build backends. A request exits nonzero before creating an output directory,
changing target options, or invoking a compiler. This includes requests whose
entrypoint is missing. Entrypoint existence and compatibility are not inspected;
they require a future backend with an explicit source-root contract.

The Go API retains `UniversalBuilder.Build(ctx, cfg, target)` and its result fields.
It returns rejected-target results together with an error. Callers must inspect
that error; a nonempty result slice does not establish a successful build.

| Result status | Error usable with `errors.Is` | Meaning |
| --- | --- | --- |
| `unavailable` | `builder.ErrBackendUnavailable` | The runtime is recognized, but its build backend is not implemented. |
| `unsupported` | `builder.ErrUnsupportedRuntime` | The runtime is not recognized. |

Recognized runtime spellings are `go`, `svelte`, `typescript`, `python`, `rust`,
`native-gpu`, `native`, `c`, and `cpp`. Every current result has `success: false`,
`optimized: false`, no artifacts, no compilation logs, and an explanatory `reason`.
`duration` measures request inspection, not compilation. Multiple selected targets
are reported in lexical name order, with joined errors preserving both categories.
At most 128 selected targets are inspected. Selecting one target in a larger
configuration remains bounded and reports that target only.

Configuration reads retain the shared one-MiB limit, regular-file and symlink
checks, and caller cancellation. Build admission performs no filesystem access.
Nil/canceled contexts, missing configuration or target selections, and oversized
selections fail without claiming rejected targets were inspected.

`PreBuildOptimizer.Plan` and `Optimize` remain available for option preparation.
The latter copies declarations into an in-memory target's options; neither method
prunes dependencies, compiles bytecode, strips artifacts, or measures performance.
Generated flags still require validation by an actual backend before execution.
`Build` does not apply these declarations while its backend is unavailable.

There is currently no registered MCP builder tool. `tools/list` is the authority
for the available surface; a `tools/call` for `standards_build` is rejected as an
unknown tool. CLI behavior does not imply an MCP execution adapter exists.

## Migration

Earlier versions returned fabricated artifact paths and successful compilation
messages without executing a compiler. Those requests now fail. Consumers must
handle the explicit error and must not use old build results as release, promotion,
or optimization evidence. Use the repository's established native build commands
until a separately verified backend provides execution and artifact readback.
This fixes false success (BUG-037 and the fabricated-output part of BUG-629);
implementing the intended universal build pipeline remains open work.
