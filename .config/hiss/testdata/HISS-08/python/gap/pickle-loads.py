import pickle


# pickle.loads reconstructs objects by invoking constructors named in the payload,
# which is arbitrary code execution driven by untrusted input.
def load(payload):
    return pickle.loads(payload)
