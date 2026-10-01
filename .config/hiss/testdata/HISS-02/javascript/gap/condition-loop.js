// The loop ends only when step reports done; no scalar bound exists, yet the condition is
// not a literal, so a line scanner cannot tell it from a bounded loop.
export function settle(step) {
  let done = false;
  while (!done) {
    done = step();
  }
}
