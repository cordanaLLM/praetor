# A backslash escapes a quote inside a triple-quoted string, raw or not, so \""" does not
# close it. Closing it there reopened a string at the real closing quotes that ran to the end
# of the file, and the recursion below was never seen.
def pattern():
    return r"""say \""" twice"""


def walk(n):
    return walk(n - 1)
