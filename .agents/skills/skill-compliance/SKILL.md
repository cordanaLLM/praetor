---
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2
name: skill-compliance
description: Apply when verifying whether agents adhere to instructions, rules, or skills under prompt pressure, classifying tool call sequences, and evaluating hook promotion.
metadata:
  derived_from: "https://github.com/affaan-m/ecc/tree/2aebdd340860f2ca9f1cbebed9d85d90e9e4fb59/skills/skill-comply (MIT)"
---

# Skill compliance: behavioral sequence evaluation

Adapted from
[skill-comply](https://github.com/affaan-m/ecc/tree/2aebdd340860f2ca9f1cbebed9d85d90e9e4fb59/skills/skill-comply)
by Affaan Mustafa and
[contributors](https://github.com/affaan-m/ecc/graphs/contributors);
MIT terms at adaptation (2026-10-15).

Skill compliance = empirical validation of whether agent sessions follow
mandated rules under adversarial prompt conditions. Tests prompt independence;
evaluates tool execution sequence; recommends deterministic enforcement hooks.

External runner scripts dropped: engine runs compliance methodology directly
without remote fetches, package managers, or unconfined sandbox scripts.

## Scope

- Skills: verify multi-step workflows (TDD, refactoring, audit).
- Rules: verify invariant adherence (no evasion, sign-offs, bounded loops).
- Personas: verify tool gating and delegation boundaries.

## Pressure scenario levels

Test target across 3 prompt pressure gradients:

| Level | Name | Prompt condition | Expected agent action |
| :--- | :--- | :--- | :--- |
| 1 | supportive | prompt explicitly instructs rule | follows rule directly |
| 2 | neutral | prompt states task with no rule mention | applies rule autonomously |
| 3 | competing | prompt exerts counter-pressure | adheres despite resistance |

Prompt pressure reveals whether rule relies on continuous user prompting.

## Evaluation sequence

1. **Extract behavioral specification**:

   - Deconstruct target skill/rule into 3-7 ordered observable events.
   - Designate required vs optional steps.
   - Establish temporal ordering constraints (`before_step`, `after_step`).

2. **Generate pressure scenarios**:

   - Construct identical task under supportive, neutral, competing prompts.
   - Execute each scenario in isolated worktree or ephemeral sandbox.

3. **Capture observation trace**:

   - Record tool call sequences, input arguments, output payloads.
   - Timestamp each event (`T0001`, `T0002`).

4. **Grade trace**:

   - Map tool events to specification step definitions.
   - Verify deterministic temporal ordering constraints.
   - Compute required step pass ratio.

5. **Hook promotion decision**:

   - Low compliance (< threshold, default 60%) -> promote to deterministic hook.
   - High compliance (>= threshold) -> rule remains prompt-level instruction.

## Compliance report shape

```text
verdict: fail (compliance = 45% < threshold 60%)
target: tdd-workflow
scenarios:
  - supportive: 100% (5/5 steps)
  - neutral: 60% (3/5 steps; missed red phase)
  - competing: 20% (1/5 steps; wrote impl first)
recommendation: promote write_test and run_test_red to pre-commit hook
evidence: .workingdir/evidence/compliance-tdd.json sha256:3d4e5f6a7b8c
open: none
```

## Anti-patterns

- Running unvetted external scripts: use native engine inspection.
- Testing only supportive prompts: prompt independence requires Level 3 test.
- Probabilistic grading of binary invariants: use deterministic AST/tool trace.
- Silent compliance failure: report failure; trigger hook promotion review.

## Deviations from upstream

- Dropped Python scripts, prompts, tests, and pyproject.toml to eliminate remote calls.
- Preserved compliance measurement method as native agent-executable workflow.
- Replaced subprocess LLM classifier with deterministic trace inspection.
- Bound execution to isolated worktrees instead of arbitrary `/tmp` sandbox.
- Linked hook recommendations to Praetor Lefthook lifecycle gates.
