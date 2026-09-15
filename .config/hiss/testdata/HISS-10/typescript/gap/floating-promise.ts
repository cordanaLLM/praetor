// The promise is neither awaited nor its rejection handled, so a rejection becomes an
// unhandled rejection at runtime. This repository carries no ESLint configuration, so
// no-floating-promises never runs.
export function start(work: () => Promise<void>): void {
  work();
}
