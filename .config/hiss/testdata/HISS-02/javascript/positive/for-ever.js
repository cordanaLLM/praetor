// for ( ; ; ) is the same unbounded loop with arbitrary spacing.
export function serve(next) {
  for ( ; ; ) {
    if (!next()) {
      break;
    }
  }
}
