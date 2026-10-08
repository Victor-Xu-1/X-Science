"""Per-PR product counter boundaries; unrelated dependency/schema versions are not inputs."""

import unittest

from scripts.packaging.product_version import ProductVersion, next_version, require_next_version


class ProductVersionTest(unittest.TestCase):
    def test_each_merge_advances_exactly_one_step(self):
        self.assertEqual(next_version("0.1.2"), "0.1.3")
        self.assertEqual(next_version("0.0.0"), "0.0.1")
        self.assertEqual(next_version("1.0.0"), "1.0.1")

    def test_patch_carry_matches_the_requested_example(self):
        self.assertEqual(next_version("0.1.98"), "0.1.99")
        self.assertEqual(next_version("0.1.99"), "0.2.0")

    def test_minor_carry_matches_the_requested_example(self):
        self.assertEqual(next_version("0.9.99"), "1.0.0")
        self.assertEqual(next_version("10.9.99"), "11.0.0")

    def test_all_minor_patch_boundaries_have_one_canonical_successor(self):
        for minor in range(10):
            for patch in range(100):
                with self.subTest(minor=minor, patch=patch):
                    old = ProductVersion.parse(f"4.{minor}.{patch}")
                    new = old.advance()
                    expected = 4 * 1000 + minor * 100 + patch + 1
                    self.assertEqual(new.major * 1000 + new.minor * 100 + new.patch, expected)
                    self.assertEqual(ProductVersion.parse(str(new)), new)
                    self.assertGreater(new, old)

    def test_invalid_or_out_of_range_authorities_fail_closed(self):
        for value in ("", "v0.1.2", "0.1", "0.1.2.3", "0.01.2", "00.1.2", "0.1.02", "0.10.0", "0.1.100", "-1.0.0", "0.1.2-beta", "0.1.2\n", " 0.1.2", None, True, 123):
            with self.subTest(value=value), self.assertRaises(ValueError):
                ProductVersion.parse(value)

    def test_direct_construction_cannot_bypass_the_counter_bounds(self):
        for parts in ((-1, 0, 0), (0, 10, 0), (0, 0, 100), (True, 1, 2), (0, 1.0, 2), (0, 1, -1)):
            with self.subTest(parts=parts), self.assertRaises(ValueError):
                ProductVersion(*parts)

    def test_transition_guard_accepts_carry_but_rejects_repeats_or_skipped_steps(self):
        for previous, candidate in (("0.1.2", "0.1.3"), ("0.1.99", "0.2.0"), ("0.9.99", "1.0.0")):
            with self.subTest(previous=previous, candidate=candidate):
                require_next_version(previous, candidate)
        for previous, candidate in (("0.1.2", "0.1.2"), ("0.1.2", "0.1.4"), ("0.1.99", "0.2.1"), ("0.9.99", "1.0.1"), ("1.0.0", "0.9.99")):
            with self.subTest(previous=previous, candidate=candidate), self.assertRaisesRegex(ValueError, "one counter step"):
                require_next_version(previous, candidate)


if __name__ == "__main__":
    unittest.main()
