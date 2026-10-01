// process.exit ends the process from a library function (owner decision Q-014).
export function fail(message) {
  console.error(message);
  process.exit(1);
}
