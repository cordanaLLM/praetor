def outer(count):
    def inner(v):
        return v + 1

    return inner(count)
