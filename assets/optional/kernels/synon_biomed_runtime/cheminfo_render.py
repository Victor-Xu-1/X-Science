from __future__ import annotations

import json
import math
import os
from pathlib import Path
from typing import Sequence


def _safe_label(label: object) -> str:
    text = str(label).strip() or "molecule"
    safe = "".join(ch if ch.isalnum() or ch in ("-", "_", ".") else "_" for ch in text)
    return safe[:96] or "molecule"


def _ensure_parent(path: str | os.PathLike[str]) -> Path:
    p = Path(path)
    p.parent.mkdir(parents=True, exist_ok=True)
    return p


def _save_rdkit_image(image, path: str | os.PathLike[str]) -> str:
    """Save an RDKit drawing result across PIL/SVG/bytes return variants."""
    p = _ensure_parent(path)
    if hasattr(image, "save"):
        image.save(str(p))
    elif isinstance(image, bytes):
        p.write_bytes(image)
    elif isinstance(image, str):
        p.write_text(image, encoding="utf-8")
    else:
        raise TypeError(f"unsupported RDKit image object: {type(image).__name__}")
    if not p.exists() or p.stat().st_size <= 0:
        raise RuntimeError(f"rendered image was not written: {p}")
    return str(p)


def _write_json(path: str | os.PathLike[str], payload: dict) -> str:
    p = _ensure_parent(path)
    p.write_text(json.dumps(payload, indent=2, ensure_ascii=False), encoding="utf-8")
    if p.stat().st_size <= 0:
        raise RuntimeError(f"metadata JSON was not written: {p}")
    return str(p)


def render_molecule_images(
    smiles: Sequence[str],
    ids: Sequence[str] | None = None,
    *,
    out_dir: str | os.PathLike[str] = ".",
    grid_filename: str = "molecules_2d_grid.png",
    mol_prefix: str = "mol_",
    use_svg: bool = True,
    mol_size: tuple[int, int] = (500, 350),
    grid_sub_img_size: tuple[int, int] = (400, 320),
    mols_per_row: int = 4,
    max_grid_molecules: int | None = None,
    metadata_filename: str | os.PathLike[str] | None = None,
) -> dict:
    """Render molecule images without a JSON sidecar by default.

    Structured metadata remains available in-memory under ``metadata``. A
    metadata file is written only when the caller explicitly supplies
    ``metadata_filename``.
    """
    from rdkit import Chem
    from rdkit.Chem import Draw

    if not smiles:
        raise ValueError("smiles must contain at least one entry")

    labels = list(ids) if ids is not None else [f"mol-{i + 1:02d}" for i in range(len(smiles))]
    if len(labels) != len(smiles):
        raise ValueError(f"ids length ({len(labels)}) does not match smiles length ({len(smiles)})")
    if mols_per_row <= 0:
        raise ValueError("mols_per_row must be positive")
    if max_grid_molecules is not None and max_grid_molecules <= 0:
        raise ValueError("max_grid_molecules must be positive when provided")
    if not isinstance(use_svg, bool):
        raise TypeError("use_svg must be a boolean")
    if metadata_filename is not None and not str(metadata_filename).strip():
        raise ValueError("metadata_filename must be non-empty when provided")

    out = Path(out_dir)
    out.mkdir(parents=True, exist_ok=True)
    valid_mols = []
    valid_labels = []
    invalid = []
    molecule_records = []
    for idx, (smi, label) in enumerate(zip(smiles, labels), start=1):
        mol = Chem.MolFromSmiles(str(smi))
        if mol is None:
            error = "RDKit could not parse SMILES"
            invalid_record = {
                "ok": False,
                "index": idx,
                "id": str(label),
                "input_smiles": str(smi),
                "smiles": str(smi),
                "error": error,
            }
            invalid.append({"index": idx, "id": str(label), "smiles": str(smi), "error": error})
            molecule_records.append(invalid_record)
            continue
        valid_mols.append(mol)
        valid_labels.append(str(label))
        molecule_records.append(
            {
                "ok": True,
                "index": idx,
                "id": str(label),
                "input_smiles": str(smi),
                "canonical_smiles": Chem.MolToSmiles(mol, canonical=True),
            }
        )

    if not valid_mols:
        raise ValueError(f"no valid SMILES were provided; invalid={invalid!r}")

    molecule_pngs = []
    molecule_svgs = []
    record_by_label = {record["id"]: record for record in molecule_records}
    for mol, label in zip(valid_mols, valid_labels):
        png_path = out / f"{mol_prefix}{_safe_label(label)}.png"
        Draw.MolToFile(mol, str(png_path), size=mol_size, legend=label)
        if not png_path.exists() or png_path.stat().st_size <= 0:
            raise RuntimeError(f"failed to render molecule PNG: {png_path}")
        if use_svg:
            svg_path = out / f"{mol_prefix}{_safe_label(label)}.svg"
            svg = Draw.MolsToGridImage([mol], molsPerRow=1, subImgSize=mol_size, legends=[label], useSVG=True)
            svg_path.write_text(str(svg), encoding="utf-8")
            if not svg_path.exists() or svg_path.stat().st_size <= 0:
                raise RuntimeError(f"failed to render molecule SVG: {svg_path}")
            molecule_svgs.append(str(svg_path))
        molecule_pngs.append(str(png_path))
        record_by_label[label]["png_path"] = str(png_path)
        if use_svg:
            record_by_label[label]["svg_path"] = str(svg_path)

    page_size = max_grid_molecules or len(valid_mols)
    grid_pages = []
    grid_base = Path(grid_filename)
    page_count = math.ceil(len(valid_mols) / page_size)
    for page_index in range(page_count):
        start = page_index * page_size
        end = start + page_size
        page_mols = valid_mols[start:end]
        page_labels = valid_labels[start:end]
        page_filename = grid_filename
        if page_count > 1:
            page_filename = f"{grid_base.stem}_page_{page_index + 1:02d}{grid_base.suffix or '.png'}"
        grid = Draw.MolsToGridImage(
            page_mols,
            molsPerRow=mols_per_row,
            subImgSize=grid_sub_img_size,
            legends=page_labels,
            useSVG=False,
        )
        grid_pages.append(_save_rdkit_image(grid, out / page_filename))

    metadata = {
        "input_count": len(smiles),
        "valid_count": len(valid_mols),
        "invalid_count": len(invalid),
        "grid_pages": grid_pages,
        "molecule_pngs": molecule_pngs,
        "molecule_svgs": molecule_svgs,
        "molecules": molecule_records,
    }
    metadata_json = None
    if metadata_filename is not None:
        metadata_json = _write_json(out / metadata_filename, metadata)
    return {
        "grid": grid_pages[0],
        "grid_pages": grid_pages,
        "molecule_pngs": molecule_pngs,
        "molecule_svgs": molecule_svgs,
        "valid_ids": valid_labels,
        "invalid_smiles": invalid,
        "molecules": molecule_records,
        "metadata": metadata,
        "metadata_json": metadata_json,
    }
