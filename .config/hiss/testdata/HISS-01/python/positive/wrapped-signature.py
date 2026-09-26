def walk(
    node,
    depth=0,
) -> int:
    # The signature closes at column 0, the way black formats a long header. That line
    # continues the header; it must not end the function before its body is read.
    if node is None:
        return depth
    return walk(node.next, depth + 1)
