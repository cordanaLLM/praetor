def is_even(n):
    # is_even and is_odd form a two-function cycle. Deciding it needs a call graph across
    # functions, which the Python line scanner does not build.
    if n == 0:
        return True
    return is_odd(n - 1)


def is_odd(n):
    if n == 0:
        return False
    return is_even(n - 1)
