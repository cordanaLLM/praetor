<!-- markdownlint-disable MD013 -->
# OpenAI Codex Context & Operating Rules
<!-- Compiled automatically by standardsctl compile-context from AGENTS.md. DO NOT EDIT DIRECTLY. -->

## Directives & Invariants

- Act on verified state, not assumption.
- Zero tolerance for destructive or un-revertible actions without authorization.
- Maintain strict DAG call graphs (zero recursion, HISS-01).
- Enforce scalar loop bounds and context timeouts on all I/O (HISS-02).
- Zero unchecked errors and zero unwrap/expect calls (HISS-07).
- Positive, negative, and boundary tests mandatory for all public interfaces (HISS-15).

## Verification Commands

```bash
make verify-all
```
