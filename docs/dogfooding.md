# Public repository dogfooding

For versioned input configurations and combined private transcript replay, use
the [configured suite](guides/dogfood-suites.md).

`praetorctl dogfood --public-loop` runs a bounded plan, apply, verification and
repeat-apply loop against curated public repositories. It retains each clone,
its upstream commit SHA, the adoption reports, and failures for reproduction.
Only Praetor runs: upstream tests, builds, hooks and agent instructions are not
executed. No changes are pushed upstream and no workstation home data is ingested.

## Run the retained loop

Run from a Praetor checkout containing its validated `.standards.lock` and local
`.config/archetypes` sources:

```bash
go run ./cmd/standardsctl dogfood --public-loop --path=. \
  --artifacts=.workingdir/evidence/public-dogfood --dry-run=false \
  --remote='https://github.com/spf13/cobra,https://github.com/pallets/flask'
```

The default is `--dry-run=true`: it retains a clone and plan with status `planned`;
it never claims `verified`. Applying requires `--dry-run=false`. The default two
applications check whether Praetor's reconciliation stabilizes. `--attempts=3`
permits one additional application if successful verification still changes the
tree. Any apply or verification error stops that repository immediately. Other
selected cases still receive results. A failed case needs a Praetor fix followed
by another invocation; earlier evidence stays in place.

Each invocation creates a fresh `public-loop-*` directory under `--artifacts`.
Its `report.json` links each retained `repo-NN/checkout` and `result.json`. A
repository result includes the original scan, source revision, planned changes,
each actual application, changed paths, tree digest, and errors. File bytes,
permissions, symlink targets, directories and directory permissions participate
in stability; Git's internal `.git` metadata does not.

The verified checks are:

- Exact lock versions and digests, using actual archetype sources from
  `--source-root` (default: `--path`). An absent or invalid source fails explicitly.
- Canonical context and all generated agent targets remain synchronized.
- HISS violations do not increase from the original upstream scan, and changed
  files have no violations. Truncated scans fail; ordinary scan exclusions remain
  explicit in each scan report.
- A repeated successful application produces the same tree digest.

This establishes these Praetor checks, not application test success or a complete
security audit of the third-party project. Existing upstream debt remains visible.

## Pin and replay a source revision

Append `#<commit SHA>` to any selected URL. The clone fetches and checks out that
exact commit and verifies the resulting `HEAD`. For example, these two revisions
were used by the first retained apply/recheck acceptance:

```bash
go run ./cmd/standardsctl dogfood --public-loop --path=. \
  --artifacts=.workingdir/evidence/public-dogfood --dry-run=false \
  --remote='https://github.com/spf13/cobra#adbc8813901bba65827259daa8e22ff94ec1f30e,https://github.com/pallets/flask#d73fa1cdcbd8b1465c151db8924ba58b1dd14e35'
```

The public loop accepts the eight existing curated repositories: gin-gonic/gin,
spf13/cobra, pallets/flask, sveltejs/template, google/googletest,
BurntSushi/ripgrep, fastify/fastify and spring-projects/spring-petclinic. URLs use
`https://github.com/`; credentials, queries, duplicates and other sources are
rejected before cloning. One run accepts 1–8 repositories, 2–3 applications,
20,000 tree entries including the root, 16 MiB per file and 256 MiB per checkout.
A clone has a two-minute deadline and the entire loop a five-minute deadline.
Git receives a fresh empty home and explicit environment, ignores global/system
configuration and credential helpers, and uses no host templates. Praetor leaves
its generated hooks inactive only in these disposable public clones. Normal
repository adoption keeps its hook activation behavior.

## Call the same loop through development MCP

Remote access requires an explicit server opt-in; client arguments alone cannot
activate it. The source launcher also supports an RPC deadline of 1–300 seconds:

```bash
python3 scripts/dev_mcp.py call --allow-remote-benchmarks --timeout 300 \
  standards_dogfood \
  '{"public_loop":true,"public_repos":"https://github.com/spf13/cobra,https://github.com/pallets/flask","artifact_dir":".workingdir/evidence/public-dogfood","dry_run":false}'
```

The response contains the same JSON report plus source/binary provenance from the
launcher. `source_root` defaults to `host_path`, then the server root.
`artifact_dir` and `source_root` obey server path confinement. `max_attempts`
accepts 2 or 3. A failed repository sets MCP `isError` and a nonzero direct-call
exit code while retaining its report. Native persistent servers can opt in with
`python3 scripts/dev_mcp.py serve --allow-remote-benchmarks` and need restarting
after source edits. Each loop creates new artifacts, so the tool is advertised
as non-idempotent even though it checks adoption stability inside each clone.

## Earlier simulation mode

`dogfood --remote=...` and `--benchmark-popular` still perform shallow-clone
adoption simulations and HISS readiness grading, then remove their clones.
Their `passed` result describes simulation execution; it does not establish an
applied and verified repository. Use `--public-loop` for retained apply/recheck
results. Remote modes and MCP dogfooding do not audit workstation skill roots.
Local `dogfood --targets=...` and self-governance checks remain available.
