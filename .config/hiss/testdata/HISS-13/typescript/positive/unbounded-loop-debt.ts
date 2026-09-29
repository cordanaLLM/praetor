// Real HISS-02 debt: a loop with no scalar upper bound. The script scanner reports it, so
// V_total moves.
export function spin(done: () => boolean): void {
  while (true) {
    if (done()) {
      return;
    }
  }
}
