import sys


# A library function ending the interpreter; only the script entry point may decide that.
def require(value):
    if not value:
        sys.exit(1)
    return value
