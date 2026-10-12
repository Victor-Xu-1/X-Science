package assets_test

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCurrentDockingPoseCommentBrandPreservesMetadata(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate repository root")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	// Execute only stdlib output formatting and selected metadata readers.
	// Do not import the docking driver, RDKit/Gemmi, or invoke Vina.
	command := exec.Command("python3", "-B", "-c", `
import ast
import csv
import hashlib
import math
import re
import runpy
import sys
import tempfile
from pathlib import Path

root = Path(sys.argv[1])
pack = root / "internal/sciencecapability/executionpacks"
skill = root / "skills/x-science/autodock-vina/scripts"
for name in ("autodock_vina.py", "autodock_vina_outputs.py"):
    assert (pack / name).read_bytes() == (skill / name).read_bytes(), name
outputs = runpy.run_path(str(pack / "autodock_vina_outputs.py"))
selected = {"pdb_atom_record", "pose_atom_records", "pose_model_texts", "affinities"}
constants = {"VINA_RESULT", "AUTODOCK_ELEMENTS"}
tree = ast.parse((pack / "autodock_vina.py").read_text())
nodes = [node for node in tree.body if
         (isinstance(node, ast.FunctionDef) and node.name in selected) or
         (isinstance(node, ast.Assign) and any(isinstance(target, ast.Name) and
          target.id in constants for target in node.targets))]
assert len(nodes) == len(selected) + len(constants)
metadata = {"Path": Path, "re": re, "math": math}
exec(compile(ast.Module(body=nodes, type_ignores=[]), "selected-pose-readers", "exec"), metadata)
atom = (f"ATOM  {1:5d} {'C':<4} {'UNL':>3} A{1:4d}    "
        f"{1.0:8.3f}{2.0:8.3f}{3.0:8.3f}{1.0:6.2f}{0.0:6.2f}          {'C':>2}\n")
pose = "MODEL 1\nREMARK VINA RESULT: -7.500 0.000 0.000\n" + atom + "ENDMDL\n"
candidate = "XScience user-supplied identity"
row = {"ligand_id": candidate, "rank": 1}
primary = {"run_index": 2, "mode_index": 3, "pdbqt_text": pose, "affinity_kcal_mol": -7.5}
with tempfile.TemporaryDirectory() as temporary:
    directory = Path(temporary)
    paths, manifest, rows = outputs["write_primary_pose_artifacts"](directory, [row], {candidate: primary})
    current = paths[0].read_text()
    header = "REMARK X-Science LIGAND " + candidate + " RANK 1 PRIMARY RUN 2 SOURCE_MODE 3\n"
    assert current == header + pose
    assert rows[0]["candidate_id"] == candidate
    assert rows[0]["sha256"] == hashlib.sha256(paths[0].read_bytes()).hexdigest()
    assert next(csv.DictReader(manifest.open()))["candidate_id"] == candidate
    historical = directory / "historical.pdbqt"
    historical.write_text(current.replace("REMARK X-Science LIGAND", "REMARK XSCIENCE LIGAND", 1))
    assert outputs["_pose_atom_count"](paths[0]) == outputs["_pose_atom_count"](historical) == 1
    assert metadata["pose_atom_records"](paths[0]) == metadata["pose_atom_records"](historical)
    assert metadata["pose_model_texts"](paths[0]) == metadata["pose_model_texts"](historical) == [pose]
    assert metadata["affinities"](paths[0]) == metadata["affinities"](historical) == [-7.5]
`, root)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("pose comment brand/metadata regression: %v\n%s", err, output)
	}
}
