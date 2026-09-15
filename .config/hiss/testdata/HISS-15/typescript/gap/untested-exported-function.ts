// An exported interface with no .test.ts beside it. `npm test` runs only the compiled
// *.test.js files that exist, so an interface with no test file is never missed, and no
// coverage gate exists for TypeScript.
export function clamp(value: number, low: number, high: number): number {
  if (value < low) {
    return low;
  }
  if (value > high) {
    return high;
  }
  return value;
}
