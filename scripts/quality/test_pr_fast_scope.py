"""Real Git/Go regressions for pull-request test scope and checkout depth."""

from pathlib import Path
import contextlib
import io
import json
import os
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from scripts.quality import pr_fast_scope as scope
from scripts.quality.runtime_test_inventory import execution_plan


def git(repo, *arguments):
    return subprocess.check_output(["git", "-C", str(repo), "-c", "user.name=scope",
                                   "-c", "user.email=scope@example.invalid", *arguments], text=True).strip()


class PackageScopeTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.directory = tempfile.TemporaryDirectory(prefix="x-science-pr-go-scope-")
        cls.repo = Path(cls.directory.name)
        files = {
            "go.mod": "module example/scope\n\ngo 1.22\n",
            "root.go": 'package scope\nimport _ "embed"\n//go:embed product-identity.json\nvar identity string\nfunc Root() int { return 1 }\n',
            "product-identity.json": "{}\n",
            "scripts/helper.go": "package scripts\nfunc Helper() {}\n",
            "lib/lib.go": "package lib\nfunc Value() int { return 1 }\n",
            "lib/lib_test.go": 'package lib\nimport ("testing"; _ "embed")\n//go:embed testdata/template_test.go\nvar template string\nfunc TestValue(t *testing.T) { if Value()!=1 { t.Fatal("value") } }\n',
            "lib/testdata/template_test.go": 'package template\n',
            "lib/external_test.go": 'package lib_test\nimport ("testing"; "example/scope/lib")\nfunc TestPublicValue(t *testing.T) { if lib.Value()!=1 { t.Fatal("value") } }\n',
            "embedded/embedded.go": 'package embedded\nimport _ "embed"\n//go:embed probe_test.go\nvar Source string\n',
            "embedded/probe_test.go": 'package embedded\n',
            "embeddedconsumer/consumer.go": 'package embeddedconsumer\nimport "example/scope/embedded"\nvar Source = embedded.Source\n',
            "app/app.go": 'package app\nimport ("example/scope"; "example/scope/lib")\nfunc Run() int { return scope.Root()+lib.Value() }\n',
            "integration/integration_test.go": 'package integration\nimport ("testing"; "example/scope/app")\nfunc TestRun(t *testing.T) { if app.Run()!=2 { t.Fatal("result") } }\n',
            "external/external.go": "package external\n",
            "external/external_test.go": 'package external_test\nimport ("testing"; "example/scope/lib")\nfunc TestValue(t *testing.T) { if lib.Value()!=1 { t.Fatal("result") } }\n',
        }
        for name, content in files.items():
            path = cls.repo / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content)
        cls.entries = scope.packages(cls.repo)
        cls.all_packages = sorted(entry["ImportPath"] for entry in cls.entries)

    @classmethod
    def tearDownClass(cls):
        cls.directory.cleanup()

    def selected(self, *paths):
        return scope.select_packages(self.repo, list(paths), self.entries)[0]

    def test_root_package_and_production_consumers_are_included(self):
        self.assertEqual(self.selected("root.go"), ["example/scope", "example/scope/app", "example/scope/integration"])
        self.assertEqual(self.selected("product-identity.json"), self.selected("root.go"))

    def test_test_only_and_external_test_imports_are_included_transitively(self):
        self.assertEqual(self.selected("lib/lib.go"), ["example/scope/app", "example/scope/external",
                                                     "example/scope/integration", "example/scope/lib"])
        self.assertEqual(self.selected("lib/testdata/input.json"), self.selected("lib/lib.go"))
        self.assertEqual(self.selected("lib/testdata/template_test.go"), self.selected("lib/lib.go"))

    def test_changed_test_files_run_their_owner_without_production_consumers(self):
        for path in ["lib/lib_test.go", "lib/external_test.go", "lib/deleted_test.go"]:
            with self.subTest(path=path):
                self.assertEqual(self.selected(path), ["example/scope/lib"])

    def test_mixed_changes_keep_only_real_production_dependency_edges(self):
        self.assertEqual(self.selected("lib/lib_test.go", "app/app.go"),
                         ["example/scope/app", "example/scope/integration", "example/scope/lib"])
        self.assertEqual(self.selected("lib/lib_test.go", "lib/lib.go"), self.selected("lib/lib.go"))

    def test_production_embedded_test_source_still_reaches_its_consumers(self):
        self.assertEqual(self.selected("embedded/probe_test.go"),
                         ["example/scope/embedded", "example/scope/embeddedconsumer"])

    def test_unknown_runtime_inputs_and_dependency_changes_broaden_instead_of_skip(self):
        for path in ["assets/optional/mcp-servers/tool.py", "skills/worker/SKILL.md",
                     ".github/workflows/quality.yml", "scripts/quality/new.py", "go.mod", "go.sum", "Makefile", ".env.example",
                     "removed/package/file.go", "removed/package/file_test.go"]:
            with self.subTest(path=path):
                self.assertEqual(self.selected(path), self.all_packages)

    def test_documentation_and_frontend_scopes_are_explicit(self):
        self.assertEqual(self.selected("LICENSE.txt", "COMMERCIAL-LICENSE.md"), [])
        self.assertFalse(scope.documentation_only("commercial_license_runtime.go"))
        self.assertEqual(self.selected("docs/guide.md", "README.md", ".github/pull_request_template.md",
                                        "frontend/README.md", "frontend/package-lock.json",
                                        "frontend/package.json"), [])
        self.assertFalse(scope.documentation_only("frontend/package-lock.json"))
        self.assertFalse(scope.documentation_only("frontend/package.json"))
        self.assertEqual(self.selected("frontend/src/App.tsx"), [])
        self.assertFalse(scope.frontend_affected(["docs/guide.md"]))
        self.assertTrue(scope.frontend_affected(["frontend/src/App.tsx"]))
        self.assertTrue(scope.frontend_affected(["product-identity.json"]))
        self.assertEqual(self.selected("frontend/go.mod"), self.all_packages)

    def test_traversal_is_rejected(self):
        for path in ["../outside.go", "/outside.go"]:
            with self.assertRaises(ValueError):
                self.selected(path)

    def test_targeted_plan_uses_existing_complete_batched_executor(self):
        names = [f"TestCase{index:03d}" for index in range(130)]
        inventory = {"example/scope": names}
        planned = execution_plan("pr", 0, False, inventory, inventory, batch_size=64)
        self.assertEqual(len(planned["commands"]), 3)
        self.assertEqual(planned["selected"], inventory)
        for command in planned["commands"]:
            self.assertIn("-count=1", command)
            self.assertIn("-timeout=30m", command)

    def test_vet_uses_the_same_affected_dependency_closure_without_test_evidence(self):
        repo = self.repo.resolve()
        argv = [
            "pr_fast_scope.py", "--repo", str(repo), "--base", "a" * 40,
            "--head", "b" * 40, "--vet",
        ]
        with patch.object(sys, "argv", argv), patch.object(
            scope, "changed_paths", return_value=["lib/lib.go"],
        ), patch.object(scope, "packages", return_value=self.entries), patch.object(
            scope, "run", return_value=subprocess.CompletedProcess([], 0),
        ) as invoke:
            self.assertEqual(scope.main(), 0)
        self.assertEqual(
            invoke.call_args.args,
            (
                repo,
                "go",
                "vet",
                "-buildvcs=false",
                "example/scope/app",
                "example/scope/external",
                "example/scope/integration",
                "example/scope/lib",
            ),
        )


class RevisionTests(unittest.TestCase):
    def test_depth_two_merge_checkout_supports_exact_base_to_merge_diff(self):
        with tempfile.TemporaryDirectory(prefix="x-science-pr-revisions-") as directory:
            repo = Path(directory) / "source"
            repo.mkdir()
            git(repo, "init", "-q", "-b", "main")
            (repo / "old.txt").write_text("rename me\n")
            git(repo, "add", ".")
            git(repo, "commit", "-qm", "initial")
            git(repo, "checkout", "-qb", "task")
            (repo / "frontend").mkdir()
            (repo / "frontend/App.tsx").write_text("export {}\n")
            git(repo, "mv", "old.txt", "new.txt")
            git(repo, "add", ".")
            git(repo, "commit", "-qm", "candidate")
            git(repo, "checkout", "-q", "main")
            (repo / "main.txt").write_text("advanced base\n")
            git(repo, "add", ".")
            git(repo, "commit", "-qm", "advanced")
            base = git(repo, "rev-parse", "HEAD")
            git(repo, "merge", "--no-ff", "-qm", "candidate merge", "task")
            head = git(repo, "rev-parse", "HEAD")
            clone = Path(directory) / "shallow"
            subprocess.run(["git", "clone", "-q", "--depth=2", repo.as_uri(), str(clone)], check=True)
            self.assertEqual(git(clone, "rev-parse", "--is-shallow-repository"), "true")
            self.assertEqual(scope.changed_paths(clone, base, head), ["frontend/App.tsx", "new.txt", "old.txt"])

    def test_unresolved_or_mutable_revision_is_rejected(self):
        with self.assertRaises(ValueError):
            scope.changed_paths(Path.cwd(), "main", "HEAD")

    def test_zero_push_base_selects_every_tracked_path(self):
        with tempfile.TemporaryDirectory(prefix="x-science-main-zero-base-") as directory:
            repo = Path(directory)
            git(repo, "init", "-q", "-b", "main")
            (repo / "go.mod").write_text("module example/zero\n\ngo 1.22\n")
            (repo / "main.go").write_text("package zero\n")
            git(repo, "add", ".")
            git(repo, "commit", "-qm", "initial")
            head = git(repo, "rev-parse", "HEAD")
            self.assertEqual(
                scope.changed_paths(repo, "0" * 40, head),
                ["go.mod", "main.go"],
            )


class FrontendProvenanceGateTests(unittest.TestCase):
    def test_frontend_provenance_only_change_still_runs_fresh_provenance_audit(self):
        repo = Path.cwd().resolve()
        argv = ['pr_fast_scope.py', '--repo', str(repo), '--base', 'a' * 40,
                '--head', 'b' * 40, '--frontend']
        with patch.object(sys, 'argv', argv), patch.object(
            scope, 'changed_paths', return_value=['frontend/MIGRATION_MANIFEST.json'],
        ), patch.object(scope, 'run', return_value=subprocess.CompletedProcess([], 0),
        ) as invoke:
            self.assertEqual(scope.main(), 0)
        self.assertEqual(invoke.call_count, 1)
        self.assertIn('audit_frontend_migration.py', invoke.call_args.args[2])

    def test_stale_frontend_provenance_fails_before_install_or_build(self):
        repo = Path.cwd().resolve()
        argv = ['pr_fast_scope.py', '--repo', str(repo), '--base', 'a' * 40,
                '--head', 'b' * 40, '--frontend']
        with patch.object(sys, 'argv', argv), patch.object(scope, 'changed_paths', return_value=['frontend/App.tsx']), patch.object(
            scope, 'run', return_value=subprocess.CompletedProcess([], 23),
        ) as invoke:
            self.assertEqual(scope.main(), 23)
        self.assertEqual(invoke.call_count, 1)
        self.assertEqual(invoke.call_args.args, (
            repo, sys.executable, 'scripts/audit/audit_frontend_migration.py', '--root', str(repo), '--check', '--require-clean',
        ))

    def test_verified_frontend_uses_related_tests_and_keeps_static_build_and_packaged_checks(self):
        argv = ['pr_fast_scope.py', '--base', 'a' * 40, '--head', 'b' * 40, '--frontend']
        with patch.object(sys, 'argv', argv), patch.object(scope, 'changed_paths', return_value=['frontend/packages/desktop/src/App.tsx']), patch.object(
            scope, 'run', return_value=subprocess.CompletedProcess([], 0),
        ) as invoke:
            self.assertEqual(scope.main(), 0)
        commands = [call.args[1:] for call in invoke.call_args_list]
        self.assertEqual(len(commands), 11)
        self.assertIn('audit_frontend_migration.py', commands[0][1])
        self.assertEqual(commands[1:], [
            ('npm', 'ci', '--ignore-scripts'),
            ('npm', 'run', 'dependencies:prepare'),
            ('npx', '--no-install', 'playwright', 'install', 'chromium'),
            ('npm', 'run', 'i18n:types'),
            ('npm', 'run', 'typecheck'), ('npm', 'run', 'lint'),
            ('npm', 'run', 'format:check'),
            ('npx', '--no-install', 'vitest', 'related', '--run', '--project', 'node', '--project', 'dom', 'packages/desktop/src/App.tsx'),
            ('npm', 'run', 'build'), ('npm', 'run', 'test:packaged'),
        ])

    def test_changed_browser_fixture_build_failure_stops_frontend_gate(self):
        argv = ['pr_fast_scope.py', '--base', 'a' * 40, '--head', 'b' * 40, '--frontend']
        fixture = ['node', '--max-old-space-size=4096', '--input-type=module', '--eval',
                   'fixture build', 'tests/web-e2e/example.fixture.html']

        def result(_repo, *command):
            return subprocess.CompletedProcess(command, 17 if list(command) == fixture else 0)

        with patch.object(sys, 'argv', argv), patch.object(scope, 'changed_paths', return_value=[
            'frontend/packages/desktop/src/App.tsx',
        ]), patch.object(scope.pr_frontend_scope, 'fixture_command', return_value=fixture), patch.object(
            scope, 'run', side_effect=result,
        ) as invoke:
            self.assertEqual(scope.main(), 17)
        commands = [call.args[1:] for call in invoke.call_args_list]
        self.assertEqual(commands[-1], tuple(fixture))
        self.assertNotIn(('npm', 'run', 'build'), commands)
        self.assertNotIn(('npm', 'run', 'test:packaged'), commands)


class EmptyGoRuntimeSetupTests(unittest.TestCase):
    """The placeholder keeps checks/evidence; only explicit JSON false skips setup."""
    def test_workflow_conditions_only_heavy_setup_on_explicit_json_false(self):
        workflow = (Path(__file__).resolve().parents[2] / ".github/workflows/quality-pr.yml").read_text()
        go_job = workflow.split("  pr-go-shards:\n", 1)[1].split("\n  pr-go-tests:", 1)[0]
        dependencies = go_job.split("      - name: Set up runtime test dependencies\n", 1)[1].split("      - name:", 1)[0]
        self.assertIn("        if: ${{ toJSON(matrix.has_go_packages) != 'false' }}\n", dependencies)
        self.assertIn("sudo apt-get install --yes bubblewrap poppler-utils", dependencies)
        self.assertIn("--requirement assets/optional/mcp-servers/bio-tools/requirements-test.txt", dependencies)
        closure = go_job.split("      - name: Test changed Go dependency closure\n", 1)[1].split("      - name:", 1)[0]
        self.assertNotIn("        if:", closure)
        self.assertIn("python3 scripts/quality/pr_fast_scope.py", closure)
        aggregate = workflow.split("  pr-go-tests:\n", 1)[1].split("\n  pr-frontend-plan:", 1)[0]
        self.assertIn("name: PR changed Go packages", aggregate)
        self.assertIn("needs: [pr-quality, pr-go-shards]", aggregate)
        self.assertIn("test '${{ needs.pr-go-shards.result }}' = 'success'", aggregate)
        self.assertIn("needs: [pr-frontend-tests, pr-go-tests, pr-quality]", workflow)
        for supplied in [{}, {"has_go_packages": None}, {"has_go_packages": False},
                         {"has_go_packages": True}, {"has_go_packages": "false"},
                         {"has_go_packages": 0}, {"has_go_packages": []}, {"has_go_packages": {}}]:
            with self.subTest(matrix=supplied):
                # toJSON preserves type; missing properties are null, not false.
                installed = json.dumps(supplied.get("has_go_packages"), separators=(",", ":")) != "false"
                self.assertEqual(installed, supplied.get("has_go_packages") is not False)

    def test_empty_scope_keeps_checks_and_artifacts_when_optional_capture_tools_are_missing(self):
        canonical = Path(__file__).resolve().parents[2]
        changed = ["docs/licenses/frontend-bundle/NOTICE.txt", "scripts/audit/audit_frontend_migration.py"]
        groups = scope.verification_scope.matched(canonical, changed)
        self.assertEqual({group["name"] for group in groups}, {"frontend-license-notices", "frontend-provenance"})
        for missing in [ModuleNotFoundError("missing scientific Python distribution"), FileNotFoundError("bwrap unavailable")]:
            with self.subTest(missing=type(missing).__name__), tempfile.TemporaryDirectory() as folder:
                root = Path(folder); repo = root / "source"; repo.mkdir(); evidence = root / "evidence"
                base, head = "a" * 40, "b" * 40
                binding = {"base": base, "head": head, "checkout_head": head, "anchor_epoch": 1,
                           "source_state": "clean", "environment": {}}
                args = ["pr_fast_scope.py", "--repo", str(repo), "--base", base, "--head", head, "--log-dir", str(evidence)]
                def forbidden(*args, **kwargs):
                    raise AssertionError("Empty-scope fixture attempted a process")
                with patch.dict(os.environ, {"GITHUB_ACTIONS": "true", "GITHUB_EVENT_NAME": "pull_request"}), \
                     patch.object(sys, "argv", args), patch.object(scope, "changed_paths", return_value=changed), \
                     patch.object(scope, "behavior_scope", return_value=(changed, [], [])), \
                     patch.object(scope.verification_scope, "matched", return_value=groups), \
                     patch.object(scope, "resolve_packages", return_value=([], "proved empty")), \
                     patch.object(scope, "planning_inputs", return_value=(binding, scope.pr_test_partition.CostProfile())), \
                     patch.object(scope, "run", return_value=subprocess.CompletedProcess([], 0)) as checks, \
                     patch.object(scope, "discover", side_effect=AssertionError("Empty scope discovered Go")), \
                     patch.object(scope.pr_go_reuse.pr_reuse_inputs, "capture", side_effect=missing) as capture, \
                     patch.object(subprocess, "run", side_effect=forbidden), \
                     patch.object(subprocess, "check_output", side_effect=forbidden), \
                     patch.object(subprocess, "Popen", side_effect=forbidden), \
                     contextlib.redirect_stdout(io.StringIO()) as output:
                    self.assertEqual(scope.main(), 0)
                self.assertEqual([list(call.args[1:]) for call in checks.call_args_list], scope.verification_scope.checks(groups))
                capture.assert_called_once_with(repo)
                self.assertIn("PR input capture unavailable", output.getvalue())
                self.assertIn("PR provenance unavailable", output.getvalue())
                self.assertEqual(json.loads((evidence / "scope.json").read_text())["packages"], [])
                self.assertEqual(json.loads((evidence / "plan.json").read_text())["commands"], [])
                self.assertEqual((evidence / "events.jsonl").read_text(), "")
                summary = json.loads((evidence / "summary.json").read_text())
                self.assertEqual(summary["exit_code"], 0)
                self.assertEqual(summary["planned_process_batches"], 0)
                self.assertFalse((evidence / "provenance.json").exists())


class PlanningEvidenceTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="x-science-pr-plan-")
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.repo = self.root / "source"
        self.repo.mkdir()
        git(self.repo, "init", "-q", "-b", "main")
        (self.repo / "go.mod").write_text("module example/plan\n\ngo 1.22\n")
        (self.repo / "fixture.go").write_text("package plan\n")
        git(self.repo, "add", ".")
        git(self.repo, "commit", "-qm", "fixture")
        self.head = git(self.repo, "rev-parse", "HEAD")

    def test_exact_checkout_and_commit_anchor_are_bound_without_wall_clock(self):
        binding, profile = scope.planning_inputs(self.repo, self.head, self.head)
        self.assertEqual(binding["base"], self.head)
        self.assertEqual(binding["head"], self.head)
        self.assertEqual(binding["checkout_head"], self.head)
        self.assertEqual(binding["anchor_epoch"], int(git(self.repo, "show", "-s", "--format=%ct", self.head)))
        self.assertEqual(binding["source_state"], "clean")
        self.assertEqual(profile.reason, "missing profile")
        self.assertEqual(profile.anchor_epoch, binding["anchor_epoch"])
        with self.assertRaisesRegex(ValueError, "checkout HEAD"):
            scope.planning_inputs(self.repo, self.head, "f" * 40)
        (self.repo / "fixture.go").write_text("package plan\n// local change\n")
        dirty, _ = scope.planning_inputs(self.repo, self.head, self.head)
        self.assertEqual(dirty["source_state"], "local-dirty")
        self.assertEqual(dirty["anchor_epoch"], binding["anchor_epoch"])

    def test_empty_scope_and_empty_partition_keep_complete_artifact_binding(self):
        binding, profile = scope.planning_inputs(self.repo, self.head, self.head)
        for inventory in [{}, {"example/plan": ["TestOne"]}, {"example/compile": []}]:
            plans = [scope.affected_test_plan(self.repo, inventory, index, 4, binding, profile)
                     for index in range(4)]
            self.assertEqual(len({plan["partition"]["assignment_sha256"] for plan in plans}), 1)
            self.assertEqual(len({plan["inventory_sha256"] for plan in plans}), 1)
            for index, plan in enumerate(plans):
                self.assertEqual(plan["partition"]["shard_index"], index)
                self.assertEqual(plan["partition"]["head"], self.head)
                if not plan["selected"]:
                    self.assertEqual(scope.execute(self.repo, plan, self.root / f"empty-{index}"), 0)
                    saved = json.loads((self.root / f"empty-{index}" / "plan.json").read_text())
                    self.assertEqual(saved, plan)
                    self.assertTrue((self.root / f"empty-{index}" / "summary.json").is_file())

    def test_main_zero_go_scope_writes_plan_in_every_partition(self):
        for index in range(4):
            evidence = self.root / f"main-{index}"
            args = ["pr_fast_scope.py", "--repo", str(self.repo), "--base", self.head, "--head", self.head,
                    "--shard-index", str(index), "--shard-count", "4", "--log-dir", str(evidence)]
            with patch.object(sys, "argv", args), patch.object(scope, "resolve_packages", return_value=([], "empty")):
                self.assertEqual(scope.main(), 0)
            plan = json.loads((evidence / "plan.json").read_text())
            self.assertEqual(plan["commands"], [])
            self.assertEqual(plan["partition"]["checkout_head"], self.head)
            self.assertEqual(plan["partition"]["shard_index"], index)

    def test_wrong_checkout_and_duplicate_inventory_fail_before_test_execution(self):
        args = ["pr_fast_scope.py", "--repo", str(self.repo), "--base", self.head, "--head", "f" * 40,
                "--log-dir", str(self.root / "evidence")]
        with patch.object(sys, "argv", args), patch.object(scope, "changed_paths", return_value=[]), patch.object(
            scope, "resolve_packages", return_value=(["example/plan"], "fixture"),
        ), patch.object(scope, "discover") as discovery, patch.object(scope, "execute") as executor:
            self.assertEqual(scope.main(), 1)
            discovery.assert_not_called()
            executor.assert_not_called()
        args[args.index("--head") + 1] = self.head
        with patch.object(sys, "argv", args), patch.object(scope, "resolve_packages", return_value=(["example/plan"], "fixture")), patch.object(
            scope, "discover", return_value={"example/plan": ["TestOne", "TestOne"]},
        ), patch.object(scope, "execute") as executor:
            self.assertEqual(scope.main(), 1)
            executor.assert_not_called()


if __name__ == "__main__":
    unittest.main()
