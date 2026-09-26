class Counter:
    def __init__(self, left):
        self.left = left

    def drain(self):
        """drain re-enters itself through the receiver."""
        if self.left == 0:
            return 0
        self.left -= 1
        return 1 + self.drain()
