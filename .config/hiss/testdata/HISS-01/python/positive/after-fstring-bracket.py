# Since Python 3.12 an f-string replacement field may reuse the string's own quote, which the
# scanner reads as the string ending, leaving the "(" below as an open bracket. A def can
# never sit inside a bracket, so the depth resets at the next def and walk is still seen.
def label(d):
    return f"{d["("]}"


def walk(n):
    return walk(n - 1)
