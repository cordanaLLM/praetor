// walk calls itself by its bare name, which HISS-01 forbids.
export function walk(node) {
  if (!node) {
    return 0;
  }
  return 1 + walk(node.next);
}
