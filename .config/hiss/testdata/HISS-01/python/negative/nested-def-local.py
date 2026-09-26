def walk(node):
    # The helper's parameter is its own local: the call inside the helper reaches it, and
    # walk itself never calls walk.
    def visit(walk):
        return walk(node)

    return visit
