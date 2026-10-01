// A parameter list wrapped one parameter per line still names the function, and the body
// below the closing parenthesis calls it.
interface Node {
  next?: Node;
}

export function visit(
  node: Node,
  depth: number,
): number {
  return node.next ? visit(node.next, depth + 1) : depth;
}
