package assets_test

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestP2RankDistinguishesNoCandidatesFromMalformedPredictions(t *testing.T) {
	runP2RankOutcomeCheck(t, `
with tempfile.TemporaryDirectory() as directory:
    path = Path(directory) / "predictions.csv"
    header = "name,rank,score,probability,center_x,center_y,center_z,residue_ids,surf_atom_ids\n"
    path.write_text(header, encoding="utf-8")
    try:
        module.parse_predictions(path)
    except ValueError as error:
        assert getattr(error, "code", None) == "p2rank_no_pockets", str(error)
        assert getattr(error, "retryable", None) is False
    else:
        raise AssertionError("zero candidates were accepted as a valid docking handoff")
    for malformed in ("", "unrelated,column\n", header + "pocket2,2,2.5,0.6,1,2,3,A_1,1\n"):
        path.write_text(malformed, encoding="utf-8")
        try:
            module.parse_predictions(path)
        except ValueError as error:
            assert getattr(error, "code", None) != "p2rank_no_pockets", str(error)
        else:
            raise AssertionError("malformed prediction output was accepted")
`)
}

func TestP2RankRetainsRawEvidenceWhenCandidateValidationFails(t *testing.T) {
	runP2RankOutcomeCheck(t, `
from types import SimpleNamespace
from unittest.mock import patch
header = "name,rank,score,probability,center_x,center_y,center_z,residue_ids,surf_atom_ids\n"
with tempfile.TemporaryDirectory() as directory:
    root = Path(directory).resolve()
    structure = root / "protein.pdb"
    structure.write_text("EXPDTA    X-RAY DIFFRACTION\nATOM      1  CA  ALA A   1       1.000   2.000   3.000  1.00 20.00           C\nEND\n", encoding="utf-8")
    archive = root / "p2rank.tar.gz"
    archive.write_bytes(b"external-engine-boundary-fixture")
    def extract_fixture(_archive, destination):
        (destination / "prank").write_text("external engine boundary", encoding="utf-8")
    def execute_fixture(command, **kwargs):
        destination = Path(command[command.index("-o") + 1])
        destination.mkdir()
        (destination / "protein_predictions.csv").write_text(header, encoding="utf-8")
        (destination / "params.txt").write_text("profile=default\n", encoding="utf-8")
        kwargs["stdout"].write("engine completed with zero candidate rows\n")
        return SimpleNamespace(returncode=0)
    previous = Path.cwd()
    try:
        os.chdir(root)
        with patch.object(module, "P2RANK_ARCHIVE_SHA256", module.sha256_file(archive)), \
             patch.object(module, "java_major", return_value=(17, "Java 17 fixture")), \
             patch.object(module, "safe_extract_archive", side_effect=extract_fixture), \
             patch.object(module.subprocess, "run", side_effect=execute_fixture), \
             patch.object(sys, "argv", [str(pack), "--structure", structure.name, "--p2rank-archive", archive.name, "--method", "P2Rank"]):
            try:
                module.main()
            except ValueError:
                pass
            else:
                raise AssertionError("empty prediction was promoted as successful")
        failures = list((root / ".p2rank-failures").iterdir())
        assert len(failures) == 1
        failure = failures[0]
        assert (failure / "p2rank_predictions.csv").read_text(encoding="utf-8") == header
        assert (failure / "p2rank_params.txt").read_text(encoding="utf-8") == "profile=default\n"
        receipt = json.loads((failure / "failure.json").read_text(encoding="utf-8"))
        assert receipt["code"] == "p2rank_no_pockets"
        assert receipt["retryable"] is False
        assert receipt["overall_pass"] is False
        assert receipt["evidence"]["p2rank_predictions.csv"]["sha256"] == module.sha256_file(failure / "p2rank_predictions.csv")
        assert not (root / module.DEFAULT_OUTPUT_DIR).exists()
        assert not (failure / "pocket_selection.json").exists()
    finally:
        os.chdir(previous)
`)
}

func runP2RankOutcomeCheck(t *testing.T, body string) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate repository root")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	pack := filepath.Join(root, "internal", "sciencecapability", "executionpacks", "p2rank_binding_pockets.py")
	bootstrap := `
import importlib.util, json, os, sys, tempfile
from pathlib import Path
sys.dont_write_bytecode = True
pack = Path(sys.argv[1])
sys.path.insert(0, str(pack.parent))
spec = importlib.util.spec_from_file_location("p2rank_pack", pack)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
`
	output, err := exec.Command("python3", "-c", bootstrap+body, pack).CombinedOutput()
	if err != nil {
		t.Fatalf("P2Rank outcome contract failed: %v\n%s", err, output)
	}
}
