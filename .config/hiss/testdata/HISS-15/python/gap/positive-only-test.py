"""Only the positive dimension is asserted. The negative dimension (a non-numeric argument)
and the boundary dimension (value exactly at low and at high) are absent, which HISS-15
forbids. Nothing in the repository measures test dimensions for Python."""

import unittest

from untested_public_function import clamp


class ClampTest(unittest.TestCase):
    def test_inside_range(self):
        self.assertEqual(clamp(5, 0, 10), 5)
