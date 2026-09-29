// walk takes a destructured parameter and calls itself by its bare name, which HISS-01 forbids.
export function walk({ node, depth = 0 }) {
  if (!node) {
    return depth;
  }
  return walk({ node: node.next, depth: depth + 1 });
}
