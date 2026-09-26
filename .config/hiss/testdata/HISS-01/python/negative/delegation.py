class Wrapper:
    def __init__(self, inner):
        self.inner = inner

    def close(self):
        # Forwarding to the same-named method on another object is delegation.
        return self.inner.close()


def close(resource):
    return resource.close()
