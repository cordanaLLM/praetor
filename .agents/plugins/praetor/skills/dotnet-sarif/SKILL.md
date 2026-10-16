---
name: dotnet-sarif
description: "Repair .NET compiler diagnostics from SARIF 2.1 build logs."
---

# .NET SARIF Build Repair

Use for .NET build diagnostics. Build output guides repair; keep warning policy.

## Practices

1. Run `dotnet build` with `-p:ErrorLog=<file>%2Cversion=2.1` for SARIF 2.1.
   `%2C` encodes comma between path and SARIF version option.
2. Read `runs[].results[]`. Inspect each result's `ruleId`, `level`, and first
   location's `artifactLocation.uri` and `region.startLine`.
3. Fix source at reported path and line, then build again. Stop after three
   rounds, on clean log, or when one round makes no progress; report results.
4. Relative `ErrorLog` paths resolve from project directory. Engine writes to
   `obj/praetor-build.sarif`; restore creates `obj/` before build.
5. Create `ErrorLog` parent directory before `CoreCompile` for custom paths.
   Missing directory can fail with CS0016.

```commands
dotnet build --no-restore -p:ErrorLog=diagnostics.sarif%2Cversion=2.1
```
