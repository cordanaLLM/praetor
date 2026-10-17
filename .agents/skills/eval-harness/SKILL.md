---
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2
name: eval-harness
description: Apply when establishing eval-driven development, defining task pass criteria, benchmarking agent reliability with pass metrics, or preventing regression across model changes.
metadata:
  derived_from: "https://github.com/affaan-m/ecc/tree/1e8c7e7994223e0ff337d1626cd08e04a1ae67ed/skills/eval-harness (MIT)"
---

# Eval harness: evaluation-driven development

Adapted from
[eval-harness](https://github.com/affaan-m/ecc/tree/1e8c7e7994223e0ff337d1626cd08e04a1ae67ed/skills/eval-harness)
by Affaan Mustafa and
[contributors](https://github.com/affaan-m/ecc/graphs/contributors);
MIT terms at adaptation (2026-10-15).

Eval harness = deterministic behavior validation for autonomous sessions.
Treats evaluations as behavioral unit tests: define criteria before coding;
run continuously; compute reliability metrics.

## Scope

- Capability evaluations: verify new behaviors agent previously failed.
- Regression evaluations: ensure existing invariant guarantees hold.
- Deterministic grading: prefer code, AST, exit codes over probabilistic judges.
- Reliability tracking: measure pass@k and pass^k stability.

## Evaluation categories

| Type | Target | Success criterion |
| :--- | :--- | :--- |
| capability | new functionality | satisfies all task assertions |
| regression | existing baseline | passes all baseline assertions |
| stability | critical path | zero flakes across k trials |

## Grader ladder

Deterministic graders override model-based graders:

1. **Code grader**: compiler, test runner, AST check, exit status.
2. **Rule grader**: schema validator, regex assertion, invariant scanner.
3. **Model judge**: prompt rubric scoring; advisory only; never release gate.
4. **Human review**: security boundary, credential handling, destructive change.

## Metrics

| Metric | Form | Definition | Target |
| :--- | :--- | :--- | :--- |
| `pass@1` | single trial | success on first attempt | baseline signal |
| `pass@3` | retries allowed | at least 1 success in 3 attempts | >= 90% capability |
| `pass^3` | stability bar | all 3 consecutive trials succeed | 100% regression |

`pass^k` enforces critical reliability: single failure fails evaluation.

## Workflow

1. **Define** (pre-code): state capability checks, regression baselines, targets.
2. **Negative proof**: execute evaluations against current code; verify failure.
3. **Implement**: apply minimal code change to satisfy criteria.
4. **Evaluate**: run deterministic test harness; compute metrics.
5. **Report**: emit summary with pass counts, trial logs, failure reasons.

Evaluation report shape:

```text
verdict: pass
capability: 3/3 passed (pass@3 = 100%)
regression: 5/5 passed (pass^3 = 100%)
graders: code = 8, model = 0
evidence: .workingdir/evidence/eval-feature-xyz.json sha256:1a2b3c4d5e6f
open: none
```

## Anti-patterns

- Proving eval with passing run only: check must fail before fix (HISS-20).
- Flaky model graders in release gates: use code graders for blocking gates.
- Optimizing prompt for single eval instance: test broad input distribution.
- Silent fallback on eval failure: surface blocker; halt turn.
- Tool attribution footers: omit AI author tags from reports.

## Deviations from upstream

- Dropped interactive slash commands (`/eval`) and unreviewed storage paths.
- Replaced subjective model graders with code and AST checkers in release gates.
- Required negative execution proof before trusting passing eval (HISS-20).
- Prohibited silent fallbacks on eval failure or model substitution.
- Stripped AI attribution footers from eval outputs and reports.
