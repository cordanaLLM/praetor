# Detailed planning drafts

`praetorctl planning prepare` compiles a typed planning draft into four linked
proposal artifacts. CLI and MCP use the same bounded compiler and metadata
report. The [architecture roadmap](../plans/shared-planning-and-reconciliation.md)
describes the later source-adapter and project-reconciliation stages.

From a source checkout, try the synthetic fixture:

```bash
praetorctl planning prepare \
  --input internal/planning/testdata/valid-draft.json \
  --output-dir .workingdir/planning-example

# Validate and render in memory without writing another directory.
praetorctl planning prepare --input .workingdir/planning-example/plan.json
```

The output directory must be new. Existing files and directories are rejected;
the command does not replace a previous plan. Use an ignored private directory
for real project inputs. The CLI accepts an explicitly selected output path;
MCP preparation additionally confines it beneath the server root's `.workingdir`.
If a write fails partway through, the error is returned and partial output stays
available for inspection. Inspect that directory before preparing a new one;
its existence does not prove a successful preparation.

The generated files are:

| Artifact | Contents |
| --- | --- |
| `plan.json` | Canonical typed draft, accepted as compiler input again |
| `TODO.md` | Proposed tasks with requirement, dependency, roadmap and milestone links |
| `ROADMAP.md` | Ordered steps, concrete actions, expected outputs and acceptance proposals |
| `MILESTONES.md` | Outcomes, dependencies, acceptance proposals and linked steps |

Recompiling canonical output produces the same digest and artifacts. A stable
draft ID identifies the plan; the content digest identifies its exact normalized
revision. Source list order and other unordered sets do not change identity.
Action order remains meaningful.

## Preparing the input

Use schema version `1` and the fixture as the complete field example. Supply a
stable draft ID, project identity and revision, source assertions, cited
requirements, milestones and detailed steps. Every step must name its
requirements and milestone, dependencies, at least two concrete actions,
expected outputs, positive/negative/boundary acceptance proposals, and unique
TODO/roadmap links. Manual, research and implementation steps are supported;
their action text is never executed by this command.
Every schema field must be present with its exact JSON spelling and type.
Use `[]` for an empty dependency set; omitted fields, `null` and alternate
capitalization are rejected. A step must reference between 1 and 32 requirements.

Keep missing information visible in the planning work before submission. The
compiler rejects incomplete structure, unsupported states, duplicate IDs,
unknown references, uncovered requirements, cycles and incompatible dependency
ordering. It does not invent a decomposition to fill missing fields.

Sources use `provenance: caller_asserted` and `verified: false`. Their IDs,
hash syntax and citation references are structurally checked. The compiler does
not read source locators, verify quoted source bytes, establish semantic truth,
or accept the plan on behalf of a person or policy. Its report therefore says
`structurally_valid`, `review_required: true` and
`provenance_status: caller_asserted_unverified`.

The first version has fixed conservative limits: 512 KiB input, 64 sources,
256 requirements, 64 milestones, 512 steps, 32 dependencies per declared node,
and 16 actions per step. Text and generated output are bounded as well. Limits
are checked before the result can be used for artifact preparation.

## MCP

`standards_planning_validate` takes `input_path` within the server root and returns
metadata without writes. `standards_planning_prepare` takes that same input plus
an `output_dir` naming a new directory under `.workingdir`. It writes the four
private artifacts and reports `artifacts_written: true` only after the writer
returns successfully. Existing output, path escapes, symlink escapes, extra
arguments and malformed input are rejected.

Both tools return the same digest, dependency ordering, provenance state and
artifact names as the CLI. They do not return the full private plan text in
their metadata response. A newly built development MCP server is required to
discover newly added tools; existing processes retain their startup snapshot.

These are draft projections in a new directory. They do not update existing
`OPEN.md`, milestone stores, human roadmaps, forge issues or project boards.
Those updates require the planned revision-checked reconciliation adapters.
Source decomposition, model invocation, step execution, completion evidence and
publication also remain outside this compiler's implemented scope.
