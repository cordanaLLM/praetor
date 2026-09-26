def walk(node):
    # The helper's parameter is its own local; it never shadows walk in walk's body.
    def visit(walk):
        return walk(node)

    return walk(node.next)
