def compute(expression):
    # eval(expression) was replaced by the explicit table lookup below.
    return TABLE[expression]


TABLE = {"a": 1, "b": 2}
