import unittest

from frontend_source_namespace import canonical_source_records, current_source_path


class SourceNamespaceTest(unittest.TestCase):
    def test_current_path_retains_original_record_and_scientific_names(self):
        record = {"path": "packages/desktop/SynonBiomed/file.ts", "sha256": "abc"}
        source = {"files": [record]}
        self.assertEqual(current_source_path(record["path"]), "packages/desktop/XScience/file.ts")
        self.assertIs(canonical_source_records(source)["packages/desktop/XScience/file.ts"], record)
        self.assertEqual(record["path"], "packages/desktop/SynonBiomed/file.ts")
        self.assertEqual(current_source_path("packages/synonym.ts"), "packages/synonym.ts")

    def test_traversal_and_collision_rejected(self):
        for value in ("", "/absolute", "../outside", "a/../b", "a//b", "a\\b"):
            with self.assertRaises(ValueError):
                current_source_path(value)
        with self.assertRaises(ValueError):
            canonical_source_records({"files": [{"path": "SynonBiomed/a"}, {"path": "XScience/a"}]})


if __name__ == "__main__":
    unittest.main()
