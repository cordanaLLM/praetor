def visit(tree):
    # Invoking the nested helper re-enters visit, so the cycle closes through it.
    def descend(child):
        return visit(child)

    return [descend(child) for child in tree.children]
