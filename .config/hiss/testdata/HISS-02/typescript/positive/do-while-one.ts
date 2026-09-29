// do ... while (1) is unbounded too; the loop is reported on its closing line.
export function pump(read: () => number | undefined): void {
  do {
    if (read() === undefined) {
      return;
    }
  } while (1);
}
