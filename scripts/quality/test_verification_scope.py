"""Keep verification-only declarations narrow and fail closed on test failures."""

import contextlib
import io
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from scripts.quality import pr_fast_scope as scope
from scripts.quality import verification_scope as contract


class VerificationOwnershipTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="x-science-verification-scope-")
        self.addCleanup(self.directory.cleanup)
        self.repo = Path(self.directory.name)
        self.contract_path = self.repo / contract.CONTRACT
        self.contract_path.parent.mkdir(parents=True)
        self.value = {"schema": "x-science.verification-scope.v1", "groups": [{
            "name": "fixture-checker", "paths": ["scripts/check.py", contract.CONTRACT], "frontend": True,
            "checks": [["python3", "-B", "scripts/test_check.py"]],
        }]}
        self.save()

    def save(self):
        self.contract_path.write_text(json.dumps(self.value), encoding="utf-8")

    def test_exact_match_drives_checks_and_frontend(self):
        groups = contract.matched(self.repo, ["scripts/check.py"])
        self.assertEqual(contract.checks(groups + groups), [["python3", "-B", "scripts/test_check.py"]])
        self.assertTrue(scope.frontend_affected(["scripts/check.py"], self.repo))
        for path in ["scripts/check.py.new", "scripts/nested/check.py", "scripts/other.py"]:
            self.assertEqual(contract.matched(self.repo, [path]), [])
            self.assertFalse(scope.frontend_affected([path], self.repo))

    def test_python_syntax_checker_is_inert_bounded_and_fail_closed(self):
        target = self.repo / "scripts/fixture.py"
        marker = self.repo / "must-not-execute"
        target.write_text(f"from pathlib import Path\nPath({str(marker)!r}).touch()\n")
        contract.check_python_syntax(self.repo, ["scripts/fixture.py"])
        self.assertFalse(marker.exists())
        target.write_text("def invalid(\n")
        with self.assertRaises(SyntaxError):
            contract.check_python_syntax(self.repo, ["scripts/fixture.py"])
        for path in ["../outside.py", "/outside.py", "scripts//fixture.py", "scripts/fixture.js"]:
            with self.assertRaises(ValueError):
                contract.check_python_syntax(self.repo, [path])
        with self.assertRaises(ValueError):
            contract.check_python_syntax(self.repo, [])

    def test_runtime_ownership_wins_and_unknown_inputs_still_expand(self):
        (self.repo / "go.mod").write_text("module example/verification\n\ngo 1.22\n")
        (self.repo / "scripts/check.py").write_text("# embedded runtime input\n")
        (self.repo / "root.go").write_text(
            'package verification\nimport _ "embed"\n//go:embed scripts/check.py\nvar Script string\n')
        (self.repo / "app").mkdir()
        (self.repo / "app/app.go").write_text('package app\nimport _ "example/verification"\n')
        entries = scope.packages(self.repo)
        expected = ["example/verification", "example/verification/app"]
        self.assertEqual(scope.select_packages(self.repo, ["scripts/check.py"], entries)[0], expected)
        self.value["groups"][0]["paths"] = ["scripts/non-runtime.py", contract.CONTRACT]
        self.save()
        self.assertEqual(scope.select_packages(self.repo, ["scripts/non-runtime.py"], entries)[0], [])
        for changed in [["scripts/unknown.py"], ["scripts/non-runtime.py", "assets/runtime.py"],
                        ["scripts/non-runtime.py", "root.go"], ["go.mod"]]:
            self.assertEqual(scope.select_packages(self.repo, changed, entries)[0], expected)

    def test_invalid_paths_cannot_claim_runtime_or_wildcard_ownership(self):
        for path in ["internal/runner.py", "../scripts/check.py", "scripts/../root.py",
                     "scripts/*.py", "scripts/check.go", "scripts/go.mod", "scripts//check.py",
                     ".github/workflows/*.yml", "docs/governance/../runtime.json", ".env", ".env.production"]:
            with self.subTest(path=path):
                self.value["groups"][0]["paths"] = [path]
                self.save()
                with self.assertRaises(ValueError):
                    contract.load(self.repo)

    def test_exact_public_templates_do_not_admit_private_environment_files(self):
        self.value["groups"][0]["paths"] += [".env.example", "docs/non-web-asset-boundary.json"]
        self.save()
        self.assertEqual(len(contract.matched(self.repo, [".env.example"])), 1)
        self.assertEqual(contract.matched(self.repo, [".env"]), [])

    def test_workflow_ownership_runs_mandatory_checks_without_runtime_exemptions(self):
        group = self.value["groups"][0]
        group["paths"] += [".github/workflows/quality.yml", "docs/governance/actions.json"]
        group["checks"] = [["bash", "scripts/check.sh"]]
        self.save()
        entries = [{"ImportPath": "fixture", "Dir": str(self.repo)}]
        self.assertEqual(scope.select_packages(self.repo, [".github/workflows/quality.yml"], entries)[0], [])
        self.assertEqual(scope.select_packages(self.repo, [".github/workflows/unknown.yml"], entries)[0], ["fixture"])
        self.assertEqual(contract.checks(contract.matched(self.repo, ["docs/governance/actions.json"])),
                         [["bash", "scripts/check.sh"]])

    def test_empty_checks_duplicate_ownership_and_malformed_contract_are_rejected(self):
        group = self.value["groups"][0]
        group["checks"] = []
        self.save()
        with self.assertRaises(ValueError):
            contract.load(self.repo)
        group["checks"] = [["python3", "-B", "scripts/test_check.py"]]
        group["paths"] *= 2
        self.save()
        with self.assertRaises(ValueError):
            contract.load(self.repo)
        self.contract_path.write_text("{invalid")
        with self.assertRaises(ValueError):
            contract.load(self.repo)

    def test_required_checker_failure_blocks_scope_execution(self):
        args = ["pr_fast_scope.py", "--repo", str(self.repo), "--base", "a" * 40, "--head", "b" * 40]
        with patch.object(sys, "argv", args), patch.object(scope, "changed_paths", return_value=["scripts/check.py"]), \
                patch.object(scope, "run", return_value=subprocess.CompletedProcess([], 17)) as invoke, \
                patch.object(scope, "packages") as discover:
            self.assertEqual(scope.main(), 17)
        discover.assert_not_called()
        invoke.assert_called_once_with(self.repo, "python3", "-B", "scripts/test_check.py")

    def test_missing_contract_has_no_exemptions(self):
        self.contract_path.unlink()
        self.assertEqual(contract.load(self.repo), [])

    def test_exact_license_notice_runs_its_checker_without_claiming_runtime_ownership(self):
        path = 'docs/licenses/frontend-bundle/NOTICE.txt'
        self.value['groups'][0]['paths'].append(path)
        self.save()
        self.assertEqual(contract.checks(contract.matched(self.repo, [path])),
                         [['python3', '-B', 'scripts/test_check.py']])
        entries = [{'ImportPath': 'fixture', 'Dir': str(self.repo)}]
        self.assertEqual(scope.select_packages(self.repo, [path], entries)[0], [])
        self.assertEqual(scope.select_packages(self.repo, ['docs/licenses/unregistered/NOTICE.txt'], entries)[0], ['fixture'])
        entries[0]['EmbedFiles'] = [path]
        self.assertEqual(scope.select_packages(self.repo, [path], entries)[0], ['fixture'])

    def test_shared_verification_runs_once_across_required_partitions(self):
        with patch.object(scope, 'changed_paths', return_value=['scripts/check.py']), patch.object(
            scope, 'resolve_packages', return_value=([], 'verification only'),
        ), patch.object(scope, 'run', return_value=subprocess.CompletedProcess([], 0)) as invoke, patch.object(
            scope, 'planning_inputs', return_value=({}, scope.pr_test_partition.CostProfile()),
        ):
            for index in range(4):
                args = ['pr_fast_scope.py', '--repo', str(self.repo), '--base', 'a' * 40,
                        '--head', 'b' * 40, '--shard-index', str(index), '--shard-count', '4']
                with patch.object(sys, 'argv', args):
                    self.assertEqual(scope.main(), 0)
            self.assertEqual(invoke.call_count, 1)

    def test_invalid_partition_cannot_skip_the_shared_verification(self):
        args = ['pr_fast_scope.py', '--repo', str(self.repo), '--base', 'a' * 40,
                '--head', 'b' * 40, '--shard-index', '4', '--shard-count', '4']
        with patch.object(sys, 'argv', args), patch.object(scope, 'changed_paths', return_value=['scripts/check.py']), \
                patch.object(scope, 'run') as invoke, contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(scope.main(), 1)
            invoke.assert_not_called()

    def test_existing_contract_cannot_remove_its_own_verification_group(self):
        for groups in [[dict(self.value["groups"][0], paths=["scripts/check.py"])], []]:
            with self.subTest(groups=groups):
                self.value["groups"] = groups
                self.save()
                with self.assertRaisesRegex(ValueError, "own verification"):
                    contract.load(self.repo)

    def test_repository_contract_contains_real_check_targets(self):
        repo = Path(__file__).resolve().parents[2]
        groups = contract.load(repo)
        self.assertTrue(groups)
        for group in groups:
            for path in group["paths"]:
                self.assertTrue((repo / path).is_file(), path)
            for command in group["checks"]:
                if "-m" in command:
                    module = command[command.index("-m") + 1]
                    modules = command[command.index("unittest") + 1:] if module == "unittest" else [module]
                    for module in modules:
                        self.assertTrue((repo / (module.replace(".", "/") + ".py")).is_file())
                else:
                    targets = [argument for argument in command[1:] if argument.startswith(("scripts/", "assets/", "skills/"))]
                    self.assertTrue(targets, command)
                    for target in targets:
                        self.assertTrue((repo / target).is_file(), target)

    def test_version_counter_has_exact_verification_ownership(self):
        repo = Path(__file__).resolve().parents[2]
        groups = contract.matched(repo, ["scripts/quality/product_version.py"])
        self.assertEqual([group["name"] for group in groups], ["pr-version-counter"])
        self.assertEqual(contract.checks(groups), [
            ["python3", "-B", "-m", "unittest", "scripts.packaging.test_product_version", "scripts.packaging.test_pr_version_gate", "scripts.packaging.test_prepare_pr_version", "scripts.packaging.test_version_config", "scripts.packaging.test_version_provenance"],
        ])

    def test_test_cost_profile_has_exact_checker_ownership_without_runtime_exemption(self):
        repo = Path(__file__).resolve().parents[2]
        path = scope.pr_test_partition.PROFILE_PATH
        groups = contract.matched(repo, [path])
        self.assertEqual([group["name"] for group in groups], ["verification-selection"])
        self.assertIn("scripts.quality.test_pr_test_partition", contract.checks(groups)[0])
        entries = [{"ImportPath": "fixture/runtime", "Dir": str(repo / "internal/runtime")}]
        self.assertEqual(scope.select_packages(repo, [path], entries)[0], [])
        self.assertEqual(scope.select_packages(repo, [path + ".new"], entries)[0], ["fixture/runtime"])
        entries[0]["Dir"] = str(repo)
        entries[0]["EmbedFiles"] = [path]
        self.assertEqual(scope.select_packages(repo, [path], entries)[0], ["fixture/runtime"])

    def test_ci_guide_is_verified_without_selecting_unrelated_runtime_packages(self):
        repo = Path(__file__).resolve().parents[2]
        paths = ["scripts/quality/README.md"]
        groups = contract.matched(repo, paths)
        self.assertTrue(contract.checks(groups), "CI documentation must retain its checker ownership")
        entries = [{"ImportPath": "fixture/runtime", "Dir": str(repo / "internal/runtime")}]
        self.assertEqual(scope.select_packages(repo, paths, entries)[0], [])

    def test_namespace_checkers_are_exact_and_keep_actual_go_ownership(self):
        repo = Path(__file__).resolve().parents[2]
        helpers = ["scripts/quality/pr_namespace_scope.py", "scripts/quality/test_pr_namespace_scope.py",
                   "scripts/quality/go_namespace_equivalence.go"]
        for path in helpers:
            groups = contract.matched(repo, [path])
            self.assertEqual([group["name"] for group in groups], ["verification-selection"])
            self.assertIn("scripts.quality.test_pr_namespace_scope", contract.checks(groups)[0])
            self.assertEqual(scope.select_packages(repo, [path], [
                {"ImportPath": "fixture/runtime", "Dir": str(repo / "internal/runtime")},
            ])[0], [])
        # If this standalone checker becomes a real Go package, actual ownership wins.
        entry = {"ImportPath": "fixture/checker", "Dir": str(repo / "scripts/quality")}
        self.assertEqual(scope.select_packages(repo, [helpers[-1]], [entry])[0], [entry["ImportPath"]])
        for path in ["scripts/quality/other.go", "scripts/quality/go_namespace_equivalence.go.old"]:
            self.assertEqual(contract.matched(repo, [path]), [])

    def test_frontend_namespace_resolver_has_its_real_provenance_checker(self):
        repo = Path(__file__).resolve().parents[2]
        for path in ["scripts/audit/frontend_source_namespace.py", "scripts/audit/test_frontend_source_namespace.py"]:
            groups = contract.matched(repo, [path])
            self.assertEqual([group["name"] for group in groups], ["frontend-provenance"])
            self.assertTrue(groups[0]["frontend"])
            self.assertIn(["python3", "-B", "scripts/audit/test_frontend_source_namespace.py"], contract.checks(groups))

    def test_connector_registration_matches_exact_independent_source_inventory(self):
        repo = Path(__file__).resolve().parents[2]
        group = next(item for item in contract.load(repo) if item["name"] == "browser-connector-packaging")
        expected = json.loads((repo / "docs/non-web-asset-boundary.json").read_text())["allowedConnectorSources"]
        actual = [path for path in group["paths"] if path.startswith("assets/")]
        self.assertEqual(sorted(actual), sorted(expected))
        self.assertTrue(group["packages"], "connector inputs must retain real runtime consumers")
        self.assertEqual(contract.matched(repo, ["assets/x-science-link/source/unregistered.js"]), [])

    def test_optional_go_owners_seed_real_consumers_and_keep_unknown_inputs_conservative(self):
        group = self.value['groups'][0]
        group['packages'] = ['fixture/cmd', 'fixture/config']
        self.save()
        entries = [
            {'ImportPath': 'fixture/cmd', 'Dir': str(self.repo / 'cmd'), 'Imports': ['fixture/config']},
            {'ImportPath': 'fixture/config', 'Dir': str(self.repo / 'config')},
            {'ImportPath': 'fixture/client', 'Dir': str(self.repo / 'client'), 'TestImports': ['fixture/config']},
            {'ImportPath': 'fixture/unrelated', 'Dir': str(self.repo / 'unrelated')},
        ]
        expected = ['fixture/client', 'fixture/cmd', 'fixture/config']
        self.assertEqual(scope.select_packages(self.repo, ['scripts/check.py'], entries)[0], expected)
        group['paths'].append('docs/governance/runner.md')
        self.save()
        self.assertEqual(scope.select_packages(self.repo, ['docs/governance/runner.md'], entries)[0], expected)
        # An actual embed retains its owner in addition to the declared roots.
        entries[-1]['Dir'] = str(self.repo)
        entries[-1]['EmbedFiles'] = ['scripts/check.py']
        all_packages = sorted(entry['ImportPath'] for entry in entries)
        self.assertEqual(scope.select_packages(self.repo, ['scripts/check.py'], entries)[0], all_packages)
        self.assertEqual(scope.select_packages(self.repo, ['scripts/check.py.old'], entries)[0], all_packages)
        group['packages'] = ['fixture/unavailable']
        self.save()
        selected, reason = scope.select_packages(self.repo, ['scripts/check.py'], entries)
        self.assertEqual(selected, all_packages)
        self.assertIn('unavailable', reason)

    def test_optional_go_owner_contract_is_strict_and_legacy_groups_remain_valid(self):
        self.assertNotIn('packages', contract.load(self.repo)[0])
        group = self.value['groups'][0]
        for owners in [[], 'fixture/cmd', ['fixture/cmd', 'fixture/cmd'], ['/fixture/cmd'],
                       ['../fixture'], ['fixture/../cmd'], ['fixture//cmd'], ['fixture/*'],
                       ['fixture/cmd@v1'], ['fixture:cmd'], ['fixture/cmd\n'], [False]]:
            with self.subTest(owners=owners):
                group['packages'] = owners
                self.save()
                with self.assertRaises(ValueError):
                    contract.load(self.repo)
        group.pop('packages')
        group['skip_runtime'] = True
        self.save()
        with self.assertRaises(ValueError):
            contract.load(self.repo)

    def test_repository_watcher_has_one_shell_checker_and_real_go_ownership(self):
        repo = Path(__file__).resolve().parents[2]
        paths = ['scripts/dev/source-backend-watch.sh', 'scripts/dev/source-backend-watch-test.sh']
        groups = contract.matched(repo, paths)
        self.assertEqual(len(groups), 1)
        self.assertEqual(groups[0]['paths'], paths)
        self.assertEqual(groups[0]['packages'], ['x-science/cmd/x-science', 'x-science/internal/config'])
        self.assertFalse(groups[0]['frontend'])
        self.assertEqual(contract.checks(groups + groups), [['bash', paths[1]]])
        entries = scope.packages(repo)  # Read the actual Go graph; do not execute Go tests.
        expected = ['x-science/cmd/x-science', 'x-science/internal/config', 'x-science/scripts/smoke-live-im']
        for path in paths:
            self.assertEqual(scope.select_packages(repo, [path], entries)[0], expected)

    def test_watcher_checker_failure_blocks_go_discovery(self):
        repo = Path(__file__).resolve().parents[2]
        argv = ['pr_fast_scope.py', '--repo', str(repo), '--base', 'a' * 40, '--head', 'b' * 40]
        paths = ['scripts/dev/source-backend-watch.sh', 'scripts/dev/source-backend-watch-test.sh']
        with patch.object(sys, 'argv', argv), patch.object(scope, 'changed_paths', return_value=paths), \
                patch.object(scope, 'run', return_value=subprocess.CompletedProcess([], 17)) as invoke, \
                patch.object(scope, 'resolve_packages', return_value=([], 'fixture')) as discover:
            self.assertEqual(scope.main(), 17)
        discover.assert_not_called()
        invoke.assert_called_once_with(repo, 'bash', paths[1])


if __name__ == "__main__":
    unittest.main()
