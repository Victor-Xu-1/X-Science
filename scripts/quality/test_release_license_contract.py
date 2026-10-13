from __future__ import annotations

import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest

try:
    from . import verification_scope
except ImportError:
    import verification_scope


PROJECT_ROOT = Path(__file__).resolve().parents[2]


class ReleaseLicenseContractTests(unittest.TestCase):
    """Exercise only the real license copy/assertions, without building or installing."""

    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="x-science-release-license-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.source = self.root / "source"
        self.package = self.root / "package"
        self.source.mkdir()
        self.package.mkdir()
        self.license_bytes = (PROJECT_ROOT / "LICENSE.txt").read_bytes()
        (self.source / "LICENSE.txt").write_bytes(self.license_bytes)

    def shell(self, statements: list[str]) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ["bash", "--noprofile", "--norc", "-euo", "pipefail", "-c", "\n".join(statements)],
            cwd=self.source,
            env={
                "PATH": os.defpath,
                "pkg": str(self.package),
                "package_dir": str(self.package),
                "install_dir": str(self.package),
            },
            capture_output=True,
            text=True,
            timeout=5,
            check=False,
        )

    def copy_license(self) -> None:
        lines = (PROJECT_ROOT / "scripts/package-release.sh").read_text(encoding="utf-8").splitlines()
        copies = [
            line for line in lines
            if re.fullmatch(r'cp LICENSE(?:\.txt)? "\$pkg/LICENSE(?:\.txt)?"', line)
        ]
        self.assertEqual(len(copies), 1, "release must retain one root license copy")
        result = self.shell(copies)
        self.assertEqual(result.returncode, 0, result.stderr)

    def consumer(
        self, script: str, variable: str, comparisons: int,
    ) -> subprocess.CompletedProcess[str]:
        lines = (PROJECT_ROOT / "scripts" / script).read_text(encoding="utf-8").splitlines()
        target = rf'"\${variable}/LICENSE(?:\.txt)?"'
        existence = [line for line in lines if re.fullmatch(rf"test -f {target}", line)]
        contents = [line for line in lines if re.fullmatch(rf"cmp LICENSE(?:\.txt)? {target}", line)]
        self.assertEqual(
            len(existence), 1, "consumer must retain root license existence check",
        )
        self.assertEqual(
            len(contents), comparisons, "consumer must retain root license content checks",
        )
        return self.shell(existence + contents)

    def consumers(self) -> tuple[tuple[str, str, int], ...]:
        return (
            ("package-release-test.sh", "package_dir", 1),
            ("install-release-test.sh", "install_dir", 0),
        )

    def test_writer_copies_canonical_license_without_alias(self) -> None:
        self.copy_license()
        self.assertEqual((self.package / "LICENSE.txt").read_bytes(), self.license_bytes)
        self.assertFalse((self.package / "LICENSE").exists())

    def test_package_accepts_writer_output(self) -> None:
        self.copy_license()
        result = self.consumer("package-release-test.sh", "package_dir", 1)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_install_accepts_writer_output(self) -> None:
        self.copy_license()
        result = self.consumer("install-release-test.sh", "install_dir", 0)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_consumers_reject_missing_license(self) -> None:
        for script, variable, comparisons in self.consumers():
            with self.subTest(script=script):
                result = self.consumer(script, variable, comparisons)
                self.assertNotEqual(result.returncode, 0, "missing canonical license was accepted")

    def test_consumers_reject_legacy_name_only(self) -> None:
        (self.package / "LICENSE").write_bytes(self.license_bytes)
        for script, variable, comparisons in self.consumers():
            with self.subTest(script=script):
                result = self.consumer(script, variable, comparisons)
                self.assertNotEqual(result.returncode, 0, "legacy-only license was accepted")

    def test_package_rejects_changed_license_bytes(self) -> None:
        self.copy_license()
        (self.package / "LICENSE.txt").write_bytes(self.license_bytes + b"\nchanged\n")
        result = self.consumer("package-release-test.sh", "package_dir", 1)
        self.assertNotEqual(result.returncode, 0, "changed legal bytes were accepted")

    def test_package_rejects_empty_license(self) -> None:
        self.copy_license()
        (self.package / "LICENSE.txt").write_bytes(b"")
        result = self.consumer("package-release-test.sh", "package_dir", 1)
        self.assertNotEqual(result.returncode, 0, "empty license was accepted")


class ReleaseLicenseRegistrationTests(unittest.TestCase):
    """Keep the bounded license regressions in the real affected-scope registry."""

    def test_exact_license_inputs_select_regressions_with_existing_owners(self) -> None:
        command = [
            "python3", "-B", "-m", "unittest",
            "scripts.quality.test_release_license_contract",
        ]
        inputs = {
            "scripts/package-release.sh": "native-package-manager-lifecycle",
            "scripts/install-release-test.sh": "native-package-manager-lifecycle",
            "scripts/package-release-test.sh": "schema-and-package-contracts",
            "scripts/quality/test_release_license_contract.py": "schema-and-package-contracts",
        }
        for path, owner in inputs.items():
            with self.subTest(path=path):
                groups = verification_scope.matched(PROJECT_ROOT, [path])
                self.assertEqual([group["name"] for group in groups], [owner])
                self.assertFalse(groups[0]["frontend"])
                self.assertEqual(verification_scope.checks(groups + groups).count(command), 1)
                if owner == "native-package-manager-lifecycle":
                    self.assertEqual(groups[0]["packages"], [
                        "x-science/internal/releasepkg", "x-science/cmd/x-science",
                    ])
                self.assertEqual(verification_scope.matched(PROJECT_ROOT, [path + ".old"]), [])


if __name__ == "__main__":
    unittest.main()
