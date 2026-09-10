<!-- markdownlint-disable MD013 -->
# GitHub Copilot Instructions: cordanaLLM/standards
<!-- Compiled automatically by standardsctl compile-context from AGENTS.md. DO NOT EDIT DIRECTLY. -->

- Ensure all Go code passes `go test -v -race ./...`.
- Strictly adhere to HISS-16 invariants (McCabe <= 10, LOC <= 75, zero unwraps).
- Do not edit generated vendor files directly; update `AGENTS.md` and run `standardsctl compile-context`.
- All public interfaces require 3D test discipline: positive, negative, and boundary cases.
