// ping and pong call each other; mutual recursion is not decided by the script scanner.
export function ping(n: number): number {
  return n > 0 ? pong(n - 1) : 0;
}

export function pong(n: number): number {
  return n > 0 ? ping(n - 1) : 0;
}
