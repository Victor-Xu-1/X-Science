"""Real RDKit rendering and the native-viewer-only helper contract."""
import importlib
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "assets/optional/kernels"))


class CheminfoRenderTest(unittest.TestCase):
    def test_exports_and_real_2d_render_do_not_need_retired_viewer(self):
        with patch.dict(sys.modules, {"py3Dmol": None}):
            helper = importlib.import_module("cheminfo_render_helpers")
            runtime = importlib.import_module("synon_biomed_runtime")
            self.assertEqual(helper.__all__, ["render_molecule_images"])
            self.assertFalse(any("3dmol" in name.lower() for name in runtime.__all__))
            with tempfile.TemporaryDirectory() as output:
                result = helper.render_molecule_images(
                    ["CCO", "not-a-smiles"], ["valid", "invalid"],
                    out_dir=output, use_svg=True,
                )
                self.assertEqual(result["valid_ids"], ["valid"])
                self.assertTrue(result["invalid_smiles"])
                self.assertTrue(Path(result["grid"]).read_bytes().startswith(b"\x89PNG"))
                self.assertIn("<svg", Path(result["molecule_svgs"][0]).read_text())
                self.assertFalse(list(Path(output).glob("*.html")))

    def test_skill_shims_export_the_same_2d_authority(self):
        import runpy
        import cheminfo_render_helpers
        for name in ("kernel.py", "cheminfo_render_helpers.py"):
            namespace = runpy.run_path(str(ROOT / "skills/synonbiomed/cheminfo-render" / name))
            self.assertEqual(namespace["__all__"], ["render_molecule_images"])
            self.assertIs(namespace["render_molecule_images"], cheminfo_render_helpers.render_molecule_images)


if __name__ == "__main__":
    unittest.main()
