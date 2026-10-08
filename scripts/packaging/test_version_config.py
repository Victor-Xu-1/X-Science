"""One root authority, registered projections, no post-merge version writer."""

from pathlib import Path
import unittest

from scripts.packaging.product_version import ProductVersion
from scripts.packaging.version_provenance import document, projections

ROOT = Path(__file__).resolve().parents[2]


class VersionConfigTest(unittest.TestCase):
    def test_every_registered_projection_has_the_current_root_version(self):
        current = document((ROOT / "product-identity.json").read_bytes())["version"]
        ProductVersion.parse(current)
        for path, pointers in projections(ROOT).items():
            for pointer in pointers:
                value = document((ROOT / path).read_bytes())
                for part in pointer.split("/")[1:]:
                    value = value[part.replace("~1", "/").replace("~0", "~")]
                self.assertEqual(value, current, (path, pointer))

    def test_legacy_writer_and_secondary_bookkeeping_are_retired(self):
        for path in (".github/workflows/version-pr.yml", ".github/release-please-config.json",
                     ".github/release-please-manifest.json", "scripts/packaging/sync_version_proposal.py"):
            self.assertFalse((ROOT / path).exists(), path)
        self.assertNotIn("release-please", (ROOT / "identity.go").read_text())

    def test_no_dependency_or_schema_version_is_a_product_projection(self):
        registered = projections(ROOT)
        self.assertEqual(registered["product-identity.json"], ["/version"])
        self.assertEqual(registered["frontend/package.json"], ["/version"])
        self.assertEqual(registered["frontend/packages/desktop/package.json"], ["/version"])
        self.assertEqual(registered["frontend/package-lock.json"],
                         ["/version", "/packages//version", "/packages/packages~1desktop/version"])
        self.assertEqual(len(registered), 4)


if __name__ == "__main__":
    unittest.main()
