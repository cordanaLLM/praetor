# Praetor Universal Dogfooding & Public Repo Benchmarking

Praetor's dogfooding engine ensures that governance rules are thoroughly self-verified against our own codebase and stress-tested against real-world external open-source repositories without mutating them.

---

## 🔍 Why Dogfood Against Non-Owned Public Repositories?

Dogfooding against arbitrary public repositories serves a dual purpose:

1. **For Praetor Developers**:
   - Continuously refines and stresses Praetor's AST scanner, archetype classification heuristics, and HISS invariants across diverse ecosystems (Go, Rust, Python, TypeScript, C++).
   - Identifies edge cases in AST parsing and debt ratcheting without risking production environments.
2. **For External Developers & Organizations**:
   - Evaluate Praetor on open-source dependencies or peer codebases before adoption.
   - Run a 100% dry-run simulation to view how Praetor would govern the repository, preview generated files, and check invariant infractions.

---

## 💻 CLI Usage (`standardsctl dogfood`)

### 1. Self-Governance Verification
Verify that your repository adheres to all HISS-16 invariants and that cross-agent context targets are synchronized:

```bash
standardsctl dogfood
```

### 2. Multi-Target Local Adoption Simulation
Simulate adoption on all repositories in your development folder in dry-run mode:

```bash
standardsctl dogfood --targets=~/dev --max-targets=15
```

### 3. Remote Non-Owned Public Repository Benchmarking
Benchmark Praetor against external public Git repositories:

```bash
# Benchmark specific public repos
standardsctl dogfood --remote=https://github.com/gin-gonic/gin,https://github.com/spf13/cobra

# Benchmark against curated popular open-source presets
standardsctl dogfood --benchmark-popular
```

#### How Remote Benchmarking Works Under the Hood:
1. **Ephemeral Shallow Clone**: Praetor performs a shallow clone (`git clone --depth 1 --single-branch`) into an isolated temporary directory.
2. **Archetype & Invariant Scan**: Executes `adopt.Adopt(..., DryRun: true)` and `hiss.Scan(...)`.
3. **Readiness Grading**: Computes an adoption grade (`A`, `B`, `C`, `F`) based on legacy debt and HISS invariant infractions.
4. **Instant Cleanup**: Removes the temporary directory immediately upon completion. Zero disk pollution, zero repository mutation.

---

## 🤖 AI Agent MCP Interface (`standards_dogfood`)

AI coding agents can run dogfooding benchmarks using the `standards_dogfood` tool:

```json
{
  "name": "standards_dogfood",
  "arguments": {
    "host_path": ".",
    "benchmark_popular": true
  }
}
```

The tool returns structured markdown reports showing context sync status, invariant audit results, and external benchmark grades.
