def apply(apply, value):
    # The parameter shadows the function; the call reaches the argument.
    return apply(value)


def step(value):
    step = int
    return step(value)


def load(path):
    from json import load
    with open(path) as handle:
        return load(handle)
