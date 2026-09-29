// process.exit ends the process from a library function whose parameter is destructured
// (owner decision Q-014).
export function fail({ message, code = 1 }) {
  console.error(message);
  process.exit(code);
}
