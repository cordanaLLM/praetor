// The parameter walk shadows the function, so the bare call reaches the argument.
export function walk(walk) {
  return walk();
}
