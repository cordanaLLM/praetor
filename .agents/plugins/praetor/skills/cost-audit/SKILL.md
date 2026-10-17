---
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2
name: cost-audit
description: Apply when investigating runaway automation loops, PR multiplication, token burn, quota bypass, or unauthorized premium model routing.
metadata:
  derived_from: "https://github.com/affaan-m/ecc/tree/1e8c7e7994223e0ff337d1626cd08e04a1ae67ed/skills/ecc-tools-cost-audit (MIT)"
---

# Cost audit: runaway automation and burn containment

Adapted from
[ecc-tools-cost-audit](https://github.com/affaan-m/ecc/tree/1e8c7e7994223e0ff337d1626cd08e04a1ae67ed/skills/ecc-tools-cost-audit)
by Affaan Mustafa and
[contributors](https://github.com/affaan-m/ecc/graphs/contributors);
MIT terms at adaptation (2026-10-15).

Cost audit = systematic containment of runaway agent loops, token spikes, and
automation recursion. Isolates root causes; prioritizes high-burn mitigations;
preserves audit evidence.

## Scope

- PR multiplication: automated jobs creating uncontrolled pull request floods.
- Quota bypass: front-door admission passing without reservation.
- Model leakage: low-tier or free requests routing to frontier model lanes.
- Infinite loops: repeated retries across identical failures or error cycles.
- Host saturation: memory/swap exhaustion across concurrent sessions.

## High-risk failure patterns

| Pattern | Mechanism | Mitigation |
| :--- | :--- | :--- |
| branch feedback | bot analyzes own generated branch | filter app-owned branch events |
| late reservation | usage counted after worker finish | reserve budget before dispatch |
| fallback escalation | cheap lane error routes to frontier | halt turn; surface blocker |
| retry cycle | deterministic error retried x3+ | anti-loop tripwire (Rule 9) |
| host thrashing | parallel builders exceed RAM bounds | hold builds below host's declared memory floor |

## Investigation ladder

1. **Freeze state**: observe active sessions, open PRs, queue length, token log.
2. **Trace ingress**: inspect trigger source (webhook, cron, dispatcher, hook).
3. **Trace execution**: verify queue consumer, worker dispatch, API credentials.
4. **Identify burn**: calculate token rate, PR generation count, host memory.
5. **Classify**: separate platform root cause from external quota limits.

## Containment sequence

Fix in strict order of burn severity:

1. **Halt feedback loops**: disable webhook trigger on automated branches.
2. **Block recursion**: trip anti-loop breaker on 3 identical errors.
3. **Plug quota leaks**: enforce pre-admission reservation checks.
4. **Stop tier leakage**: lock model selection; deny unreviewed frontier dispatch.
5. **Shed host load**: check available memory and swap against host's declared floor before next build.

## Operational rules

- Read-only diagnosis first: never run mutating scripts to prove bug.
- Halt on repeated failure: >= 3 identical AST diffs or errors stops unit.
- Local verification: reproduce leak using offline harness or mock server.
- No silent fallback: reporting substitution required if tier unavailable.

Report shape:

```text
verdict: contained
leak: recursive PR creation on bot branches (24 PRs/hour)
root_cause: webhook handler lacked sender filter for internal automation
fix: added sender exclusion guard in dispatcher
verified: offline replay blocked synthetic bot event; zero egress
open: none
```

## Deviations from upstream

- Generalized from sibling repository to engine and fleet multi-agent workflows.
- Dropped external dependencies on missing ECC-native skill modules.
- Enforced engine anti-loop rules (halt on >= 3 repeats) and memory gates.
- Required read-only diagnosis; prohibited mutating scripts to verify burn.
- Prohibited silent model fallback or unverified lane downgrade.
