# ADR-0018: Forge Drivers Repaired in Place Instead of a Spec-Driven Provider Engine

## Status

Accepted — 2026-10-18. Supersedes [ADR-0008](0008-spec-driven-provider-integration.md).
Amends the dependency citation of [ADR-0016](0016-figures-for-adopters.md).

## Context

ADR-0008 (Proposed, 2026-09-12) planned a spec-driven provider engine: provider
descriptors under `.config/providers/`, an offline OpenAPI compiler in a separate
`tools/specc` module, and a generic runtime under `internal/provider` that would
replace the hand-written forge drivers. The same day, the owner decided that the
confirmed forge findings are fixed in place in the existing drivers, which ADR-0008
item OD.2 also records.

A clause-by-clause read of ADR-0008 against main (commit `b4c2c497`, 2026-10-18)
found 5 of its 62 clauses practised, 8 partly practised, 5 superseded and 44 not
practised:

- `.config/providers/`, `tools/specc` and `internal/provider` do not exist.
- The forge drivers in `internal/forge` (GitHub, GitLab, Gitea/Forgejo) and
  `internal/milestone/forge.go` were repaired in place with negative fixtures and
  are the production drivers.
- The root `go.mod` has two external dependencies, `go.yaml.in/yaml/v3` and
  `github.com/pelletier/go-toml/v2`, so ADR-0008's "exactly `gopkg.in/yaml.v3`"
  no longer holds.

## Decision

1. **The hand-written drivers stay.** `internal/forge` and `internal/milestone`
   are the provider integration. Each keeps its own HTTP code behind an injected
   client, with request deadlines, response and error-body ceilings, and bounded
   pagination. Their tests remain the conformance evidence.
2. **One token resolver.** Authentication uses `util.ResolveAuthTokenContext`
   (`internal/util/util.go`): explicit token, `GITHUB_TOKEN`, `GH_TOKEN`, then a
   bounded `gh auth token`, under the caller's deadline.
3. **No generated provider code at runtime.** The binaries ship no generated
   client for any provider, as ADR-0008 decided.
4. **Dependency policy.** The root module has few external dependencies, each a
   pinned parser: `go.yaml.in/yaml/v3` and `github.com/pelletier/go-toml/v2`. A new
   external dependency is the exception and is recorded where it is added. This
   replaces ADR-0008's "keeps exactly `gopkg.in/yaml.v3`", the wording that
   ADR-0016 ("Constraints" and decision 3) cites.
5. **What ADR-0008 planned and nobody built** (descriptors, the offline compiler,
   the generic runtime, provider CLI and MCP tools, provider lockfile entries) is
   not pursued. These reusable parts exist on their own and are used where they
   fit: Ed25519 receipts (`internal/lockdown/receipts.go`), injection
   neutralisation (`internal/lockdown/sanitize.go`), and spend limits for model
   routing (`internal/router/limits.go`).
6. **Revisit trigger.** A second provider API surface beyond the three forges
   (for example a package registry or an infrastructure API) that the fleet needs
   to call. Then a new record weighs a descriptor-driven engine against more
   hand-written drivers, using the measured upkeep of the drivers this record
   keeps.

## Consequences

### Positive

- The records describe the code that runs: no Proposed record claims modules that
  do not exist.
- The single-dependency wording that ADR-0016 cites is corrected to the policy
  main follows.

### Negative / Trade-offs

- Each new forge capability is written by hand, in each driver that needs it.
- Rate limiting and pagination stay per driver; no shared governor sees
  concurrent calls across drivers.

### Neutral

- ADR-0009 (structural unification) records the same driver route in its status
  note.

## References

- [ADR-0008: Spec-Driven Provider Integration](0008-spec-driven-provider-integration.md) (superseded)
- [ADR-0009: Structural Unification](0009-structural-unification.md)
- [ADR-0016: Figures for adopters](0016-figures-for-adopters.md)
- The clause-by-clause read (2026-10-18) is kept in the private, Git-ignored
  `.workingdir/` evidence of the operations clone.
