"""CPython emits `SyntaxWarning: "is" with 'int' literal` when compiling this module."""


def is_one(value):
    return value is 1
