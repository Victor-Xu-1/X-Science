"""One PDB residue instance defines the movable partition, never its name alone."""
from rdkit import Chem

MAX_CONTEXT_ATOMS = 20000
WATER_RESIDUES = {"DOD", "HOH", "H2O", "WAT"}


def pdb_residue_key(atom):
    info = atom.GetPDBResidueInfo()
    if info is None:
        return None
    return (info.GetResidueName().strip().upper(), info.GetChainId().strip(),
            int(info.GetResidueNumber()), info.GetInsertionCode().strip())


def _partition(molecule, indices, label):
    selected = set(indices)
    fixed = [atom.GetIdx() for atom in molecule.GetAtoms() if atom.GetIdx() not in selected]
    protein = any(atom.GetIdx() not in selected and atom.GetPDBResidueInfo() is not None
                  and not atom.GetPDBResidueInfo().GetIsHeteroAtom() for atom in molecule.GetAtoms())
    return indices, fixed, protein, label


def select_pdb_ligand(molecule, ligand_residue_name=None, ligand_atom_serial=None):
    if molecule.GetNumAtoms() > MAX_CONTEXT_ATOMS:
        raise ValueError("protein-ligand context exceeds the bounded atom limit")
    if ligand_atom_serial is not None:
        atoms = [atom for atom in molecule.GetAtoms() if atom.GetPDBResidueInfo() is not None
                 and atom.GetPDBResidueInfo().GetSerialNumber() == ligand_atom_serial]
        if not atoms:
            raise ValueError("specified ligand atom is not present in the current pose")
        if len(atoms) != 1:
            raise ValueError("PDB atom serials are missing or ambiguous")
        key = pdb_residue_key(atoms[0])
        if ligand_residue_name and key[0] != ligand_residue_name:
            raise ValueError("specified ligand atom does not belong to the requested ligand residue")
        indices = [atom.GetIdx() for atom in molecule.GetAtoms() if pdb_residue_key(atom) == key]
        name, chain, number, insertion = key
        return _partition(molecule, indices, f"{name}:{chain or '-'}:{number}{insertion}")
    if ligand_residue_name:
        groups = {}
        for atom in molecule.GetAtoms():
            key = pdb_residue_key(atom)
            if key and key[0] == ligand_residue_name:
                groups.setdefault(key, []).append(atom.GetIdx())
        if not groups:
            raise ValueError("specified ligand residue is not present in the current pose")
        if len(groups) != 1:
            raise ValueError("the protein complex contains multiple ligand candidates; open one ligand pose before minimizing")
        return _partition(molecule, next(iter(groups.values())), f"{ligand_residue_name}:*")

    protein_indices = []
    groups = {}
    standalone = []
    for atom in molecule.GetAtoms():
        key, info = pdb_residue_key(atom), atom.GetPDBResidueInfo()
        if key and key[0] in WATER_RESIDUES:
            continue
        if info is not None and not info.GetIsHeteroAtom():
            protein_indices.append(atom.GetIdx())
            continue
        standalone.append(atom.GetIdx())
        if key:
            groups.setdefault(key, []).append(atom.GetIdx())
    if not protein_indices:
        if not standalone:
            raise ValueError("input contains no ligand")
        # Preserve the standalone legacy contract; explicit UI requests use a native atom.
        return _partition(molecule, standalone, None)
    candidates = [(key, indices) for key, indices in groups.items()
                  if sum(molecule.GetAtomWithIdx(i).GetAtomicNum() > 1 for i in indices) >= 2
                  and any(molecule.GetAtomWithIdx(i).GetAtomicNum() == 6 for i in indices)]
    if not candidates:
        raise ValueError("input contains no ligand")
    if len(candidates) != 1:
        raise ValueError("the protein complex contains multiple ligand candidates; open one ligand pose before minimizing")
    (name, chain, number, insertion), indices = candidates[0]
    return _partition(molecule, indices, f"{name}:{chain or '-'}:{number}{insertion}")


def prepare_pdb_selection(content, ligand_atom_serial=None):
    """Parse only the requested alternate, retaining the original output records."""
    lines = content.splitlines()
    if sum(line.startswith("MODEL ") for line in lines) > 1:
        raise ValueError("current pose contains multiple PDB models; choose one active model before minimizing")
    if ligand_atom_serial is None:
        return content
    atoms = [line.ljust(80) for line in lines if line.startswith(("ATOM  ", "HETATM"))]
    matches = [line for line in atoms if int(line[6:11]) == ligand_atom_serial]
    if not matches:
        raise ValueError("specified ligand atom is not present in the current pose")
    if len(matches) != 1:
        raise ValueError("PDB atom serials are missing or ambiguous")
    selected = matches[0]
    def record_key(line):
        return (line[17:20].strip().upper(), line[21].strip(), int(line[22:26]), line[26].strip())
    key = record_key(selected)
    alternate = selected[16].strip()
    alternatives = {line[16].strip() for line in atoms if record_key(line) == key and line[16].strip()}
    if not alternate and len(alternatives) > 1:
        raise ValueError("selected ligand conformer is ambiguous")
    result = []
    for raw in lines:
        if raw.startswith(("ATOM  ", "HETATM")) and record_key(raw.ljust(80)) == key:
            line = raw.ljust(80)
            if line[16].strip() and line[16].strip() != alternate:
                continue
            # RDKit normally ignores non-A alternate labels. Only its parse copy changes.
            raw = line[:16] + " " + line[17:]
        result.append(raw)
    return "\n".join(result) + "\n"
