"""Published input authority for sequence and deposited-chain PDB searches."""
# Adapted from AIPOCH Open Science80a262e structures-pdb{,-similarity}.ts.
# Changed for the existing Python MCP SDK; original Apache-2.0 notice:
# docs/licenses/open-science-pdb-search-80a262e/NOTICE.md.

SEQUENCE_INPUT_SCHEMA = {
    "type": "object",
    "additionalProperties": False,
    "properties": {
        "sequence": {"type": "string", "minLength": 25, "maxLength": 50_000},
        "identity_cutoff": {"type": "number", "minimum": 0, "maximum": 1, "default": 0.3},
        "evalue_cutoff": {"type": "number", "exclusiveMinimum": 0, "default": 0.1},
        "min_query_coverage": {"type": "number", "minimum": 0, "maximum": 1, "default": 0},
        "max_candidates": {"type": "integer", "minimum": 1, "maximum": 1000, "default": 100},
        "max_rows": {"type": "integer", "minimum": 1, "maximum": 25, "default": 10},
    },
    "required": ["sequence"],
}

SIMILARITY_INPUT_SCHEMA = {
    "type": "object",
    "additionalProperties": False,
    "properties": {
        "pdb_id": {"type": "string", "pattern": "^[1-9][A-Za-z0-9]{3}$"},
        "label_asym_id": {
            "type": "string", "minLength": 1, "maxLength": 16,
            "pattern": "^[A-Za-z0-9]+$",
        },
        "max_rows": {"type": "integer", "minimum": 1, "maximum": 50, "default": 10},
    },
    "required": ["pdb_id", "label_asym_id"],
}
