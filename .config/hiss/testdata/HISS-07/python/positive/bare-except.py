# A bare except catches and suppresses every exception, including the ones it should not.
def load(path):
    try:
        return open(path).read()
    except:
        return ""
