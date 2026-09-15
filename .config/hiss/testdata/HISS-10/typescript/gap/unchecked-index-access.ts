// values[0] is typed string even for an empty array: noUncheckedIndexedAccess is not
// enabled, so the compiler reports nothing and the call throws at runtime.
export function firstLength(values: string[]): number {
  return values[0].length;
}
