// A counted loop is bounded, and the unbounded shape inside a string or comment is text.
// while (true) {}
export function sum(values, limit) {
  let total = 0;
  for (let i = 0; i < limit && i < values.length; i += 1) {
    total += values[i];
  }
  return total + "while (true)".length;
}
