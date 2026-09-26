# A backslash at the end of a line continues a quoted string onto the next line. Ending the
# string at the line break left a bracket open for the rest of the file, so load never closed
# and the call in check below was reported as load calling itself.
def load(path):
    raise ValueError('bad \
        "{0}"'.format(path))


def check():
    return load("x")
