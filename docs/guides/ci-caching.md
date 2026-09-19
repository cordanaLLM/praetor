# Why every CI job names its own Go build cache

`actions/setup-go` can manage the Go caches itself, and until this change every workflow in
this repository let it. That is the wrong shape for a repository with more than one job, and
the reason is in the action's own source:

```ts
const primaryKey = `setup-go-${platform}-${arch}-${linuxVersion}go-${versionSpec}-${fileHash}`;
const cacheKey = await cache.restoreCache(cachePaths, primaryKey);
```

`fileHash` is the hash of `go.mod`. The key carries no workflow name and no job name, there are
no `restore-keys`, and `cachePaths` is both `GOMODCACHE` and `GOCACHE`. Three consequences
follow, and this repository had all three:

1. **Every job shares one entry.** Nine workflows resolved to the identical key
   `setup-go-Linux-x64-ubuntu24-go-1.27.1-73c9e98d…` (measured on `ubuntu-24.04`, which is
   why the key names `ubuntu24`; the jobs now run on `ubuntu-26.04`, so that exact string is a
   record of the run, not a key any run produces today). On a cold key the job that finishes
   first writes it. Compliance finishes in 0.8 min and Security in 1.25 min; the verification
   gate takes 13 min. The gate therefore restored a build cache produced by jobs that never
   compile with `-race` and never build a linter.
2. **A hit suppresses the save.** `actions/cache` skips saving when the primary key matched
   exactly, which the log states plainly: `Cache hit occurred on the primary key …, not saving
   cache.` So the entry froze at whatever the first cold run happened to contain, and no later
   run could improve it.
3. **The heavier the job, the worse it does.** The job with most to cache is the least likely
   to win the race to write, and is guaranteed not to save once it starts hitting.

Together these cost the verification gate roughly 390 s per run recompiling the race-instrumented
dependency graph from nothing. Measured on run `35093440528`: the `docdistill` package reported
`1.334s` of test time behind a 220 s gap, which is compilation, not testing.

## The shape that replaces it

`cache: false` on `setup-go`, and `.github/actions/go-cache` restoring two caches with
different key disciplines, because the two caches want different things:

| Cache | Key | Shared between jobs? |
| :--- | :--- | :--- |
| `GOMODCACHE` | runner OS, runner image, arch, Go version, `go.sum` hash | Yes — the module set is a pure function of `go.sum` |
| `GOCACHE` | runner OS, runner image, arch, Go version, job name, `go.sum` hash, **commit SHA** | No — jobs compile with different flags |

Both keys name the runner image *in addition to* `runner.os`, not instead of it. `runner.os` is
the literal `Linux` on every Ubuntu image, so it cannot tell 24.04 from 26.04; the image comes
from the `ImageOS` variable the runner exports (`ubuntu24`, `ubuntu26`, `macos26`, `win25`).
Upstream `setup-go` composes its key the same way — `RUNNER_OS` as the platform, then `ImageOS`
appended as `linuxVersion` on Linux. Without the image the `ubuntu-24.04` to `ubuntu-26.04` move
would have gone on restoring objects built against the previous image's toolchain and C library;
without `runner.os` a runner that exports no `ImageOS` falls back to the literal `unknown`, and
two such runners on different operating systems would share one key.

The Python wheel cache in `ci.yml` is keyed the same way, on `runner.os`, the go-cache action's
`image` output and the interpreter version resolved by `setup-python`.

The commit SHA in the build-cache key is what makes it roll. Every run misses its primary key,
so every run *saves*; the `restore-keys` prefixes then hand it the newest cache built by the
same job. A stable key would be an exact hit, and an exact hit never saves.

## What keeps the shape

`forge.AuditGoBuildCaches` reports any job that leaves the cache to `setup-go`, and any two
jobs that name one build cache. Two guard tests in `internal/forge` run it against this
repository's own workflows and against the workflow templates under `templates/go/`, so an
adopted repository cannot inherit the defect either. Both are verified by mutation rather than
by watching them pass: reverting the workflows, pointing two jobs at one cache name, and
restoring `cache: true` in a shipped template each turn the guard red.

Adopters carry the two `actions/cache` steps inline, because an adopted repository has no copy
of this repository's composite action.
