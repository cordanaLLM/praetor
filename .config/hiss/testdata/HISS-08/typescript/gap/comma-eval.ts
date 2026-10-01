// (0, eval)(code) is indirect eval: eval is not followed by its own argument list.
export function evaluate(expression: string): unknown {
  return (0, eval)(expression);
}
