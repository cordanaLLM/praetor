// eval of a runtime string in TypeScript.
export function evaluate(expression: string): unknown {
  return eval(expression);
}
