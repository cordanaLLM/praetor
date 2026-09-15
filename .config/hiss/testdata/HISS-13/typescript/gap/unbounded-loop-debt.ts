// Real HISS-02 debt: a loop with no scalar upper bound. No .ts dispatch exists in the
// scanner, so V_total does not move.
export function spin(done: () => boolean): void {
  while (true) {
    if (done()) {
      return;
    }
  }
}
