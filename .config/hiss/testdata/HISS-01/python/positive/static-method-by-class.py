class Tree:
    @staticmethod
    def count(n):
        """A static method has no receiver; the class name reaches it."""
        if n == 0:
            return 0
        return 1 + Tree.count(n - 1)
