---
name: python-dev
description: "Write and check Python with pinned command-line tools, typed boundaries, and property tests."
---

# Python Development (`python-dev`)

Use for Python creation, review, or changes. Report unavailable tools: `uv`, `ruff`, `mypy`, `pytest`.

## Practices

1. One-off tools: PEP 723 inline metadata in script; run with `uv run --script <file>`. Add dependencies: `uv add --script <file> <pkg>`.
2. Ruff lint: JSON diagnostics via `ruff check --output-format json`; formatting: `ruff format --check`. Fix each finding before handoff.
3. Mypy type check: JSON diagnostics via `mypy -O json` (`--output json`). Resolve diagnostics at source; silence only with code reason.
4. Hypothesis properties at parsing and serialization boundaries. Use `@given` and `hypothesis.strategies` for valid, invalid, and boundary inputs.
5. Pydantic validation at each external input: `Model.model_validate_json(raw)`. Avoid `json.loads` plus manual field checks.

## Check Your Work

Run from project environment. Keep JSON diagnostics for review. Add focused pytest cases for positive, negative, boundary behavior; include Hypothesis properties for data boundaries.

```commands
uv run --script scripts/tool.py
uv add --script scripts/tool.py requests
ruff check --output-format json .
ruff format --check .
mypy -O json .
pytest -q
```
