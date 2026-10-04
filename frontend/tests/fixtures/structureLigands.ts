/** Synthetic coordinates for preview behavior, not a docking/experimental result. */
export const MULTI_LIGAND_PDB = [
  'ATOM      1  N   GLY A   1       0.000   0.000   0.000  1.00 20.00           N',
  'ATOM      2  CA  GLY A   1       1.400   0.000   0.000  1.00 20.00           C',
  'ATOM      3  C   GLY A   1       2.000   1.400   0.000  1.00 20.00           C',
  'ATOM      4  O   GLY A   1       3.200   1.500   0.000  1.00 20.00           O',
  'ATOM      5  N   ALA A   2       1.200   2.400   0.000  1.00 20.00           N',
  'ATOM      6  CA  ALA A   2       1.600   3.800   0.000  1.00 20.00           C',
  'ATOM      7  C   ALA A   2       3.000   4.100   0.000  1.00 20.00           C',
  'ATOM      8  O   ALA A   2       3.400   5.200   0.000  1.00 20.00           O',
  'TER',
  'HETATM    9  C1  UNL A 101       2.000   1.000   3.000  1.00 20.00           C',
  'HETATM   10  O1  UNL A 101       3.200   1.000   3.000  1.00 20.00           O',
  'HETATM   11  C1  UNL A 102       2.000   1.000   3.000  1.00 20.00           C',
  'HETATM   12  O1  UNL A 102       3.200   1.000   3.000  1.00 20.00           O',
  'HETATM   13  C1  UNL B 101       2.000   1.000   3.000  1.00 20.00           C',
  'HETATM   14  O1  UNL B 101       3.200   1.000   3.000  1.00 20.00           O',
  'END',
].join('\n');

export const MULTI_LIGAND_MODELS =
  [1, 2]
    .map(
      (model) => 'MODEL     ' + String(model).padStart(4) + '\n' + MULTI_LIGAND_PDB.replace(/\nEND$/, '\n') + 'ENDMDL\n'
    )
    .join('') + 'END\n';

export const MULTI_LIGAND_CIF = [
  'data_synthetic',
  'loop_',
  ...[
    'group_PDB',
    'id',
    'type_symbol',
    'label_atom_id',
    'label_alt_id',
    'label_comp_id',
    'label_asym_id',
    'label_entity_id',
    'label_seq_id',
    'pdbx_PDB_ins_code',
    'Cartn_x',
    'Cartn_y',
    'Cartn_z',
    'occupancy',
    'B_iso_or_equiv',
    'auth_seq_id',
    'auth_comp_id',
    'auth_asym_id',
    'auth_atom_id',
    'pdbx_PDB_model_num',
  ].map((field) => '_atom_site.' + field),
  ...MULTI_LIGAND_PDB.split('\n')
    .filter((line) => /^(ATOM  |HETATM)/.test(line))
    .map((line) => {
      const atom = line.slice(12, 16).trim();
      const residue = line.slice(17, 20).trim();
      const chain = line.slice(21, 22).trim();
      const seq = line.slice(22, 26).trim();
      const protein = line.startsWith('ATOM');
      const asym = protein ? 'A' : chain + seq;
      return [
        protein ? 'ATOM' : 'HETATM',
        line.slice(6, 11).trim(),
        line.slice(76, 78).trim(),
        atom,
        '.',
        residue,
        asym,
        protein ? '1' : '2',
        protein ? seq : '.',
        '?',
        line.slice(30, 38).trim(),
        line.slice(38, 46).trim(),
        line.slice(46, 54).trim(),
        '1.0',
        '20.0',
        seq,
        residue,
        chain,
        atom,
        '1',
      ].join(' ');
    }),
  '#',
].join('\n');
