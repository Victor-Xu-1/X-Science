"""The real Structures MCP surface for the two bounded PDB search methods."""
from __future__ import annotations

import json
from pathlib import Path
import sys
import unittest

import anyio

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "lib"))
from mcp_structures_interactions.server import mcp

NEW_METHODS = {"pdb_search_sequence", "pdb_search_structure_similarity"}
OLD_METHODS = {
    "pdb_search_structures", "pdb_get_structures", "pdb_get_entities",
    "pdb_get_ligands", "pdb_select_latest_liganded_structure",
    "alphafold_get_prediction", "alphafold_check_coverage",
    "emdb_get_entries", "emdb_get_entry_section", "emdb_get_validation",
    "emdb_search_entries", "complexportal_get_complexes",
    "complexportal_search_by_participant", "intact_build_network",
    "intact_fetch_interactions", "intact_get_interaction_details", "intact_get_interactor",
}


class PDBSearchCatalogContract(unittest.TestCase):
    def tools(self):
        return [tool.model_dump(by_alias=True) for tool in anyio.run(mcp.list_tools)]

    def test_both_searches_are_discoverable_once_and_keep_existing_tools(self):
        tools = self.tools()
        names = [tool["name"] for tool in tools]
        self.assertEqual(len(names), len(set(names)))
        self.assertTrue(NEW_METHODS <= set(names), NEW_METHODS - set(names))
        self.assertTrue(OLD_METHODS <= set(names), OLD_METHODS - set(names))
        for tool in tools:
            if tool["name"] in NEW_METHODS:
                self.assertIs(tool["annotations"]["readOnlyHint"], True)

    def test_discovery_has_the_exact_strict_bounded_inputs(self):
        schemas = {tool["name"]: tool["inputSchema"] for tool in self.tools()}
        self.assertTrue(NEW_METHODS <= schemas.keys(), NEW_METHODS - schemas.keys())
        sequence = schemas["pdb_search_sequence"]
        similarity = schemas["pdb_search_structure_similarity"]
        for schema in (sequence, similarity):
            self.assertIs(schema["additionalProperties"], False)
            self.assertEqual(schema["type"], "object")
        self.assertEqual(sequence["required"], ["sequence"])
        self.assertEqual(set(sequence["properties"]), {
            "sequence", "identity_cutoff", "evalue_cutoff", "min_query_coverage", "max_candidates", "max_rows",
        })
        self.assertEqual(sequence["properties"]["sequence"]["maxLength"], 50_000)
        self.assertEqual(sequence["properties"]["max_candidates"], {
            "type": "integer", "minimum": 1, "maximum": 1000, "default": 100,
        })
        self.assertEqual(sequence["properties"]["max_rows"]["maximum"], 25)
        self.assertEqual(sequence["properties"]["evalue_cutoff"]["exclusiveMinimum"], 0)
        self.assertEqual(similarity["required"], ["pdb_id", "label_asym_id"])
        self.assertEqual(set(similarity["properties"]), {"pdb_id", "label_asym_id", "max_rows"})
        self.assertEqual(similarity["properties"]["pdb_id"]["pattern"], "^[1-9][A-Za-z0-9]{3}$")
        self.assertEqual(similarity["properties"]["label_asym_id"]["maxLength"], 16)
        self.assertEqual(similarity["properties"]["max_rows"]["maximum"], 50)

    def test_domain_roster_matches_actual_registered_methods(self):
        domains = json.loads((ROOT / "lib/mcp_bio/domains.json").read_text())
        names = {tool["name"] for tool in self.tools()}
        self.assertEqual(set(domains["structures-interactions"]), names)
        self.assertTrue(NEW_METHODS <= names, NEW_METHODS - names)


if __name__ == "__main__":
    unittest.main()
