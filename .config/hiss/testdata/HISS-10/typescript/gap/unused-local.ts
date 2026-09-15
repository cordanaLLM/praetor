// The local is never read. noUnusedLocals is not enabled, so tsc says nothing.
export function total(values: number[]): number {
  const unusedScale = 2;
  return values.reduce((acc, n) => acc + n, 0);
}
