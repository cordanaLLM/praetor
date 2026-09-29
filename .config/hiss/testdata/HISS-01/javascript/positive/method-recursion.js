// A method reaches itself through this, so depth re-enters depth.
export class Tree {
  depth(node) {
    return node ? 1 + this.depth(node.child) : 0;
  }
}
