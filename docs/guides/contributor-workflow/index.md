# Contributing, hooks and CI

These guides cover working on Praetor itself: the verification a change must pass, the Git
hooks that run it locally, the development MCP server and how CI caches builds.

Read [Contributing to Praetor](../contributing.md) first.

- [Contributing to Praetor](../contributing.md): code standards, local verification and
  what a pull request must carry.
- [Local Git hooks](../git-hooks.md): installing Lefthook and what each hook runs.
- [Develop against this checkout's MCP](../development-mcp.md): `scripts/dev_mcp.py` builds
  this checkout and exposes its MCP server over stdio or a direct call.
- [Checkpoint cadence](../checkpoint-cadence.md): how unfinished work becomes visible in Git
  and draft review.
- [CI caching](../ci-caching.md): why every CI job names its own Go build cache.
