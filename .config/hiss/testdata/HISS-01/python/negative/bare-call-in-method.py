def render(value):
    return str(value)


class View:
    def __init__(self, value):
        self.value = value

    def render(self):
        # Class scope is skipped by name lookup: this reaches the module-level render.
        return render(self.value)

    @staticmethod
    def build(value):
        return render(value)
