# __import__ loads a module chosen at runtime: dynamic code loading.
def load(name):
    return __import__(name)
