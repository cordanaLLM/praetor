// Indirect eval through an alias is dynamic execution the call shape does not reveal.
const run = globalThis.eval;

export function compute(expression) {
  return run(expression);
}
