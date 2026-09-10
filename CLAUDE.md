<!-- markdownlint-disable MD013 -->
# Claude Code Guidelines: cordanaLLM/standards
<!-- Compiled automatically by standardsctl compile-context from AGENTS.md. DO NOT EDIT DIRECTLY. -->

## Commands

```bash
go test -v -race ./...
go run ./cmd/standardsctl compile-context --verify
go run ./cmd/standardsctl audit
make verify-all
```

## Architectural Invariants (HISS-16)

- **Acyclic Control Flow (HISS-01)**: Recursion strictly prohibited; call graph must be DAG.
- **Bounded Loops & Timeouts (HISS-02)**: Scalar upper bounds on loops; context timeout on all I/O.
- **Complexity Caps (HISS-04)**: McCabe Cyclomatic <= 10, Cognitive <= 15, Func LOC <= 75.
- **Zero Unchecked Errors (HISS-07)**: Zero .unwrap() / .expect(); handle all errors explicitly.
- **Zero Warnings (HISS-10)**: Compilers and linters must pass with zero warnings.
- **3D Testing (HISS-15)**: Positive, negative, and boundary tests mandatory.
- **Context Integrity (HISS-16)**: Single canonical AGENTS.md source.

## Behavioral Invariants

- **Lead with Action**: Return code changes and commands directly without conversational preamble.
- **Diagnostic Distillation**: Limit compiler/linter error feedback to <= 1500 tokens with line pointers.
- **No Evasion**: Never bypass hooks or use --no-verify.
