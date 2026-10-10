"""Selection-only regressions: no force field or original task execution."""
import unittest

from rdkit import Chem
import structure_minimization as minimization
from structure_minimization_selection import prepare_pdb_selection


def duplicate_name_pose():
    molecule = Chem.RWMol()
    for serial, number, hetero in ((1, 1, False), (2, 10, True), (3, 20, True)):
        atom = Chem.Atom("C")
        info = Chem.AtomPDBResidueInfo()
        info.SetName(" C1 ")
        info.SetSerialNumber(serial)
        info.SetResidueName("UNL" if hetero else "ALA")
        info.SetChainId("A")
        info.SetResidueNumber(number)
        info.SetIsHeteroAtom(hetero)
        atom.SetMonomerInfo(info)
        molecule.AddAtom(atom)
    return molecule.GetMol()


class LigandInstanceSelection(unittest.TestCase):
    def test_name_only_cannot_silently_combine_distinct_residues(self):
        with self.assertRaisesRegex(ValueError, "multiple ligand"):
            minimization.select_pdb_ligand(duplicate_name_pose(), "UNL")

    def test_native_atom_selects_only_its_complete_residue(self):
        selected, fixed, protein, label = minimization.select_pdb_ligand(
            duplicate_name_pose(), "UNL", 3
        )
        self.assertEqual(selected, [2])
        self.assertEqual(fixed, [0, 1])
        self.assertTrue(protein)
        self.assertEqual(label, "UNL:A:20")

    def test_atom_and_declared_residue_must_agree(self):
        with self.assertRaisesRegex(ValueError, "requested ligand residue"):
            minimization.select_pdb_ligand(duplicate_name_pose(), "OTHER", 3)

    def test_missing_atom_cannot_fall_back_to_another_residue(self):
        with self.assertRaisesRegex(ValueError, "not present"):
            minimization.select_pdb_ligand(duplicate_name_pose(), "UNL", 99)

    def test_multiple_models_are_not_silently_parsed_or_written_as_the_first(self):
        with self.assertRaisesRegex(ValueError, "multiple PDB models"):
            prepare_pdb_selection("MODEL        1\nENDMDL\nMODEL        2\nENDMDL\n", 3)

    def test_selected_alternate_is_not_silently_replaced_by_A(self):
        source = ("HETATM    1  C1  UNL A 101       0.000   0.000   0.000  1.00 20.00           C\n"
                  "HETATM    2  O1 AUNL A 101       1.100   0.000   0.000  0.50 20.00           O\n"
                  "HETATM    3  O1 BUNL A 101       0.000   1.100   0.000  0.50 20.00           O\nEND\n")
        prepared = prepare_pdb_selection(source, 3)
        self.assertNotIn("HETATM    2", prepared)
        self.assertIn("HETATM    3  O1  UNL", prepared)
        molecule = Chem.MolFromPDBBlock(prepared, removeHs=False)
        self.assertIsNotNone(molecule)
        self.assertEqual([atom.GetPDBResidueInfo().GetSerialNumber() for atom in molecule.GetAtoms()], [1, 3])
        with self.assertRaisesRegex(ValueError, "conformer is ambiguous"):
            prepare_pdb_selection(source, 1)


if __name__ == "__main__":
    unittest.main()
