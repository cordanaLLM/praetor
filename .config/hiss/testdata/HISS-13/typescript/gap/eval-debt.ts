// Real HISS-08 debt: dynamic eval of a runtime string. The repository's own semgrep rule
// hiss-08-banned-eval covers TypeScript and reports it, but semgrep findings never enter the
// baseline and the scanner has no .ts dispatch at all, so V_total does not move.
export function run(expr: string): unknown {
  return eval(expr);
}
