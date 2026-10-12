from __future__ import annotations

import pathlib
import sys
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1] / "lib"))

from chebi_ontology.tool import _normalize_compound


class ChEBISynonymContractTests(unittest.TestCase):
    def test_published_scientific_field_is_not_a_product_namespace(self):
        record = _normalize_compound({
            "names": {
                "SYNONYM": [{"name": "retained scientific synonym"}, {"name": ""}],
                "IUPAC NAME": [{"name": "retained IUPAC name"}],
            },
        })
        self.assertEqual(record["synonyms"], ["retained scientific synonym"])
        self.assertEqual(record["iupac_names"], ["retained IUPAC name"])


if __name__ == "__main__":
    unittest.main()
