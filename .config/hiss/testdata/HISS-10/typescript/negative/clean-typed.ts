// Fully annotated and null-checked: nothing for the compiler to say.
export function firstLength(values: string[]): number {
  const head = values[0];
  return head === undefined ? 0 : head.length;
}
