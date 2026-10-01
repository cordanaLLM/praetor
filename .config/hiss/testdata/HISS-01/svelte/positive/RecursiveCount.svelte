<script lang="ts">
  // count calls itself by its bare name inside the component's script block.
  type Node = { children: Node[] };
  export let tree: Node;

  function count(node: Node): number {
    return 1 + node.children.map((child) => count(child)).reduce((a, b) => a + b, 0);
  }
</script>

<p>{count(tree)}</p>
