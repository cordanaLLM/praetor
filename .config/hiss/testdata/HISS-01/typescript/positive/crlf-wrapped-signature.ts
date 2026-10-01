// Pinned CRLF (-text in .gitattributes): the carriage return that ends each line of a Windows
// checkout must not hide a header whose parameter list wraps onto the lines below it.
interface Node {
  next?: Node;
}

export function visit(
  node: Node,
  depth: number,
): number {
  return node.next ? visit(node.next, depth + 1) : depth;
}
