---
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2
name: preparatory-refactoring
description: Separate a behaviour-preserving reshape from the feature or fix it prepares.
---

# Preparatory Refactoring

Trigger: feature or fix touches code over HISS-04 caps, or needs reshape before change fits.

1. Reshape alone -> commit subject `refactor:`.
2. Preserve behaviour and test assertions. Keep every test function.
3. Check range with `praetorctl forge refactor-check --base <rev> --head <rev>`. REF-02 assertion lock runs by default. CI adds `--run-tests` for REF-01 suite invariance.
4. Refactor passes -> feature or fix in separate commit.

HISS-04 caps: McCabe cyclomatic <= 10; cognitive complexity <= 15; function length <= 60 lines of code; <= 50 statements. Function length = audit ceiling `config.AuditMaxFuncLOC`; other caps = `gocyclo`, `gocognit`, `funlen` in `make lint`.

Conflicting repository, profile, facet, operator rules -> strictest value. See [Sources and strictness](../../../docs/guides/effective-policy.md#sources-and-strictness) and [Strictness lattice](../../../docs/guides/archetype-authoring.md#3-the-strictness-lattice-highest-standard-wins).
