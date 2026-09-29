// isEven and isOdd call each other. A cycle through two functions needs a call graph,
// which the script scanner does not build.
export function isEven(n) {
  return n === 0 ? true : isOdd(n - 1);
}

export function isOdd(n) {
  return n === 0 ? false : isEven(n - 1);
}
