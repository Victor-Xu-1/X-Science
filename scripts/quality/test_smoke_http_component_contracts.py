"""Exercise smoke component assertions offline, without starting an HTTP server."""

import copy
import json
import os
import pathlib
import subprocess
import tempfile
import unittest
import zipfile


ROOT = pathlib.Path(__file__).resolve().parents[2]
SMOKE = ROOT / "scripts/smoke-http.sh"


class SmokeContractTestCase(unittest.TestCase):
    def setUp(self):
        self.script = SMOKE.read_text(encoding="utf-8")
        self.setup = self.script.split('binary="${1:-dist/x-science}"\n', 1)[1].split('\nport="$(', 1)[0]
        self.temporary = tempfile.TemporaryDirectory(prefix="x-science-smoke-contract-")
        self.addCleanup(self.temporary.cleanup)
        self.fixture = pathlib.Path(self.temporary.name)

    def statement(self, predicate):
        matches = [line for line in self.script.splitlines() if predicate(line)]
        self.assertEqual(len(matches), 1, "expected exactly one smoke assertion")
        return matches[0] + "\n"

    def run_statement(self, statement, environment=None, cwd=ROOT):
        return subprocess.run(
            ["bash", "--noprofile", "--norc", "-c", "set -euo pipefail\n" + self.setup + "\n" + statement],
            cwd=cwd,
            env={
                "PATH": os.defpath,
                "HOME": str(self.fixture),
                "PYTHONDONTWRITEBYTECODE": "1",
                **(environment or {}),
            },
            text=True,
            capture_output=True,
            timeout=10,
        )

    def assert_accepts(self, statement, environment=None):
        result = self.run_statement(statement, environment)
        self.assertEqual(result.returncode, 0, result.stderr)

    def assert_rejects(self, statement, environment=None):
        result = self.run_statement(statement, environment)
        self.assertNotEqual(result.returncode, 0, result.stdout)


class ConnectorSmokeContractTests(SmokeContractTestCase):
    def setUp(self):
        super().setUp()
        inventory_path = ROOT / "assets/x-science-link/manifest.json"
        inventory = json.loads(inventory_path.read_text(encoding="utf-8"))
        package, = inventory["files"]
        self.package = inventory_path.parent / package["path"]
        self.checksum = self.statement(lambda line: "| sha256sum -c -" in line)
        self.version = self.statement(lambda line: line.startswith('unzip -p "$link_package" manifest.json'))

    def test_config_targets_inventory_package(self):
        config = self.fixture / "smoke.json"
        writer = self.statement(lambda line: line.startswith("printf '{\"host\":\"127.0.0.1\""))
        self.assert_accepts(writer, {
            "port": "9", "plugin_dir_json": '"/fixture/plugin"',
            "adapter_origin": "http://127.0.0.1:9", "cfg": str(config),
        })
        configured = json.loads(config.read_text(encoding="utf-8"))["x_science_link_package"]
        self.assertEqual(ROOT / configured, self.package)
        self.assertTrue((ROOT / configured).is_file())

    def test_checksum_accepts_tracked_archive(self):
        self.assert_accepts(self.checksum, {"link_package": str(self.package)})

    def test_checksum_rejects_corrupted_archive(self):
        corrupt = self.fixture / "corrupt.zip"
        data = bytearray(self.package.read_bytes())
        data[-1] ^= 1
        corrupt.write_bytes(data)
        self.assert_rejects(self.checksum, {"link_package": str(corrupt)})

    def test_version_accepts_tracked_archive(self):
        self.assert_accepts(self.version, {"link_package": str(self.package)})

    def test_version_rejects_wrong_version(self):
        wrong = self.fixture / "wrong-version.zip"
        with zipfile.ZipFile(wrong, "w") as archive:
            archive.writestr("manifest.json", json.dumps({"version": "wrong"}))
        self.assert_rejects(self.version, {"link_package": str(wrong)})

    def test_version_rejects_missing_version(self):
        missing = self.fixture / "missing-version.zip"
        with zipfile.ZipFile(missing, "w") as archive:
            archive.writestr("manifest.json", "{}")
        self.assert_rejects(self.version, {"link_package": str(missing)})

    def test_expectations_follow_component_and_identity_authorities(self):
        inventory_dir = self.fixture / "assets/x-science-link"
        source_dir = inventory_dir / "source"
        source_dir.mkdir(parents=True)
        inventory = {"source": "assets/x-science-link/source", "files": [{
            "path": "fixture package.zip", "sha256": "a" * 64,
        }]}
        (inventory_dir / "manifest.json").write_text(json.dumps(inventory), encoding="utf-8")
        (source_dir / "manifest.json").write_text(json.dumps({"version": "fixture-version"}), encoding="utf-8")
        (self.fixture / "product-identity.json").write_text(json.dumps({"machine_slug": "fixture-product"}), encoding="utf-8")
        result = self.run_statement(
            'printf "%s\\n" "$link_package_path_json" "$link_package_sha256" "$link_package_version" "$native_plugin_id" "$native_plugin_mount"\n',
            cwd=self.fixture,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.splitlines(), [
            json.dumps("assets/x-science-link/fixture package.zip"), "a" * 64,
            "fixture-version", "fixture-product", "/api/plugins/fixture-product",
        ])


class NativePluginSmokeContractTests(SmokeContractTestCase):
    def setUp(self):
        super().setUp()
        slug = json.loads((ROOT / "product-identity.json").read_text(encoding="utf-8"))["machine_slug"]
        host = (ROOT / "internal/plugins/host/host.go").read_text(encoding="utf-8")
        for marker in ('mount := "/api/plugins/" + identity.MachineSlug',
                       'ID:           identity.MachineSlug,', 'APIMount:     mount,',
                       'PluginFormat: "x_science_agent_native",'):
            self.assertIn(marker, host)
        mount = "/api/plugins/" + slug
        self.native = {
            "id": slug, "pluginFormat": "x_science_agent_native",
            "server": {"apiMount": mount}, "modules": [{"endpoint": mount + "/capabilities"}],
        }
        self.identity = self.statement(lambda line: line.startswith("printf '%s' \"$plugins\"")
                                       and "id" in line and "terminal-tools" not in line)
        self.mount = self.statement(lambda line: line.startswith("printf '%s' \"$plugin_manifest\"")
                                    and "apiMount" in line)
        self.format = self.statement(lambda line: line.startswith("printf '%s' \"$plugin_manifest\"")
                                     and "pluginFormat" in line)
        self.capability = self.statement(lambda line: line.startswith("printf '%s' \"$plugins\"")
                                         and "/capabilities" in line)

    def environment(self, native=None):
        native = self.native if native is None else native
        return {
            "plugins": json.dumps({"plugins": [native]}, separators=(",", ":")),
            "plugin_manifest": json.dumps(native, separators=(",", ":")),
        }

    def test_id_accepts_authoritative_native_fixture(self):
        self.assert_accepts(self.identity, self.environment())

    def test_id_rejects_wrong_native_fixture(self):
        wrong = copy.deepcopy(self.native)
        wrong["id"] += "-wrong"
        self.assert_rejects(self.identity, self.environment(wrong))

    def test_mount_accepts_authoritative_native_fixture(self):
        self.assert_accepts(self.mount, self.environment())

    def test_mount_rejects_wrong_native_fixture(self):
        wrong = copy.deepcopy(self.native)
        wrong["server"]["apiMount"] += "-wrong"
        self.assert_rejects(self.mount, self.environment(wrong))

    def test_format_check_is_retained(self):
        self.assert_accepts(self.format, self.environment())
        wrong = copy.deepcopy(self.native)
        wrong["pluginFormat"] = "wrong"
        self.assert_rejects(self.format, self.environment(wrong))

    def test_capability_check_is_retained(self):
        self.assert_accepts(self.capability, self.environment())
        wrong = copy.deepcopy(self.native)
        # The existing grep checks a path substring; remove that path entirely.
        wrong["modules"][0]["endpoint"] = "/wrong/capabilities"
        self.assert_rejects(self.capability, self.environment(wrong))


class SmokeVerificationRegistrationTests(unittest.TestCase):
    def test_component_regression_runs_with_existing_smoke_syntax_gate(self):
        contract = json.loads((ROOT / "scripts/quality/verification_scope.json").read_text(encoding="utf-8"))
        group = next(item for item in contract["groups"] if item["name"] == "http-smoke-contract")
        self.assertEqual(group["paths"], [
            "scripts/smoke-http.sh", "scripts/quality/test_smoke_http_component_contracts.py",
        ])
        self.assertEqual(group["checks"], [
            ["bash", "-n", "scripts/smoke-http.sh"],
            ["python3", "-B", "-m", "unittest", "scripts.quality.test_smoke_http_component_contracts"],
        ])
        self.assertEqual(group["packages"], [
            "x-science/cmd/x-science", "x-science/internal/server", "x-science/internal/config",
        ])
        self.assertFalse(group["frontend"])


if __name__ == "__main__":
    unittest.main()
