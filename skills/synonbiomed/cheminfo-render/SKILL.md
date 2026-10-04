---
name: cheminfo-render
description: "Render 2D molecule PNG/SVG grids with RDKit and publish native protein/ligand structures for the workbench Mol* viewer. Documents render_molecule_images(), immutable structure references, and the synon.structure-scene.v1 comparison manifest. Load before preparing chemical figures or structure previews."
license: Apache-2.0
---

# Cheminfo Render

Use the manifest-verified Python helper for 2D drawings. For 3D, publish the
actual coordinate artifacts and use the workbench's native Mol* viewer.
Do not generate a standalone HTML viewer, install another viewer, or download
browser rendering libraries inside a scientific task. The Python environment
does not own the interactive 3D renderer.

## 2D molecule images

```python
from cheminfo_render_helpers import render_molecule_images

result = render_molecule_images(
    ["CCO", "CC(=O)O"],
    ids=["candidate-a", "candidate-b"],
    grid_filename="molecules_2d_grid.png",
    use_svg=True,
)
```

The keyword contract is `out_dir`, `grid_filename`, `mol_prefix`, `use_svg`,
`mol_size`, `grid_sub_img_size`, `mols_per_row`, `max_grid_molecules`, and
`metadata_filename`. Do not invent other rendering flags.
`use_svg=True` writes one SVG per valid molecule; `use_svg=False` writes
PNGs only. Metadata is returned in memory. A JSON sidecar is written only
when `metadata_filename` is supplied. Publish needed files using the
advertised save_artifacts tool schema, not an assumed Python function.

Read candidate identifiers and SMILES from the actual validated candidate
table or SDF used for computation. Do not reconstruct them from memory or
substitute another molecule with the same label.

Do not import rdMolDraw2D from rdkit.Chem, set unsupported fontSize options,
call PrepareAndDrawInPNG, or assume a drawing result has an img.data field.

## Native 3D structure preview

1. Save the real PDB/mmCIF/SDF coordinate artifacts with save_artifacts and
   retain the returned immutable artifact version IDs. The native Mol*
   preview is opened from those artifacts; an HTML export is not required.
2. Keep component/pose-score/ranking tables with their structures. For docking,
   publish the complex ensemble and its component CSV together. Use the native
   pose controls to select or compare ligands in the fixed receptor frame.
   Do not refit ligand coordinates merely to improve a picture.
   A structure preview loads only the clicked coordinate file. Multiple ligands
   in one PDB/mmCIF are separate chain/residue/conformer instances; clicking a
   ligand previews it alone with the same receptor. Overlay comparison requires
   an explicit user selection. Different poses in PDBQT/SDF use model controls.
3. Open the native structure preview to check loaded structures, selections,
   camera and labels. A successful file write does not prove a rendered scene.
   If a reviewed preview image is required, export/capture the actual Mol*
   scene and retain that image's saved version and SHA-256. A 2D molecule grid
   is not a screenshot of a 3D scene.
4. For a multi-structure comparison, publish the existing structure-scene
   manifest described below. It references saved structure versions; it does
   not replace the coordinate files or create new computation results.

## Multi-structure scene manifest

Use exactly the existing `synon.structure-scene.v1` fields below. This is an
illustrative contract example: replace every version ID, name and image hash
with the values from the actual saved artifacts before publication.
`preview_image` is an object, not a filename string.
`derived_structures` and `layers` are the recognized fields, not
`derived_versions` or `visible_layers`.

```json
{
  "schema": "synon.structure-scene.v1",
  "scene_id": "comparison",
  "mother_structure": {"name": "receptor.pdb", "version_id": "receptor-version"},
  "derived_structures": [
    {"name": "complex.pdb", "version_id": "complex-version"}
  ],
  "layers": [
    {"version_id": "receptor-version", "role": "mother", "representation": "cartoon", "visible": true},
    {"version_id": "complex-version", "role": "derived", "representation": "ball-and-stick", "visible": true}
  ],
  "preview_image": {
    "name": "scene.png",
    "version_id": "preview-version",
    "sha256": "REPLACE_WITH_ACTUAL_IMAGE_SHA256"
  }
}
```

Every structure reference has `name` and `version_id` matching a saved
snapshot. Mother and derived versions are distinct; each has a visible layer
with a representation in the recorded comparison. This manifest is provenance,
not permission to inject its files into every coordinate preview. Open the
referenced artifacts individually on the file board, or put one receptor and
separately identified ligands in a single coordinate file for native selection.
Do not duplicate the receptor or conflate ligand instances by rewriting all
their chain/residue/conformer identities. The preview image must match its actual immutable hash
and be inspected through `read_file` when independent review is enabled.
The workbench Mol* Snapshot action saves the actual render as a new image
artifact derived from the displayed immutable structure, without replacing the
coordinate file. Use that saved image's version and hash in the scene manifest.
The independent reviewer uses the normal immutable artifact read path; do not
invoke a retired review tool. Report unavailable capture/review capabilities
honestly instead of using a 2D grid as 3D evidence.
