"""Real-worker regressions for discarded synchronous process outcomes."""
from pathlib import Path


class WorkerProcessOutcomeCases:
    """Cases consumed by the canonical real-pipe WorkerProtocolTests fixture."""
    def test_discarded_nonzero_results_are_executed_failures(self):
        cases = {
            "shell": "os.system('exit 7')",
            "shell alias": "launch = os.system\nlaunch(command='exit 7')",
            "run": "subprocess.run(command, capture_output=True, text=True)",
            "call": "subprocess.call(command)",
            "wait": "process = subprocess.Popen(command)\nprocess.wait()",
            "communicate": (
                "process = subprocess.Popen(command, stdout=subprocess.PIPE, "
                "stderr=subprocess.PIPE, text=True)\nprocess.communicate()"
            ),
        }
        for name, invocation in cases.items():
            with self.subTest(name=name):
                result = self.cell(
                    "import os, subprocess, sys\n"
                    "command = [sys.executable, '-c', "
                    "\"import sys; print('child-diagnostic', file=sys.stderr); sys.exit(7)\"]\n"
                    + invocation + "\nopen('false-success', 'w').write('bad')"
                )
                self.assertIsNone(result["preflight"])
                self.assertIn("CalledProcessError", result["error"] or "")
                self.assertIn("exit status 7", result["error"] or "")
                self.assertFalse(Path(self.directory.name, "false-success").exists())
                if name not in ("shell", "shell alias"):
                    self.assertEqual(result["stderr"].count("child-diagnostic"), 1)
        repaired = self.cell("subprocess.run([sys.executable, '-c', \"print(42)\"])")
        self.assertIsNone(repaired["error"])
        self.assertEqual(repaired["stdout"].strip(), "42")

    def test_consumed_status_and_caught_failure_keep_fallback_semantics(self):
        result = self.cell(
            "import os, subprocess, sys\n"
            "status = os.system('exit 3')\n"
            "assert os.waitstatus_to_exitcode(status) == 3\n"
            "if subprocess.call([sys.executable, '-c', 'raise SystemExit(2)']) != 0:\n"
            "    print('call-fallback')\n"
            "result = subprocess.run([sys.executable, '-c', 'raise SystemExit(4)'])\n"
            "assert result.returncode == 4\n"
            "try:\n"
            "    os.system('exit 5')\n"
            "except subprocess.CalledProcessError as cause:\n"
            "    assert cause.returncode == 5\n"
            "    print('caught-fallback')\n"
        )
        self.assertIsNone(result["error"])
        self.assertEqual(result["stdout"], "call-fallback\ncaught-fallback\n")

    def test_success_and_ordinary_call_frames_are_unchanged(self):
        result = self.cell(
            "import os, subprocess, sys\n"
            "os.system('exit 0')\n"
            "subprocess.run([sys.executable, '-c', \"import sys; print('error is data', file=sys.stderr)\"])\n"
            "exec('local_value = 42')\n"
            "assert local_value == 42\n"
            "def ordinary():\n    return 99\n"
            "ordinary()\n"
            "class Parent:\n"
            "    def call(self):\n        return 9\n"
            "class Child(Parent):\n"
            "    def call(self):\n"
            "        super().call()\n"
            "        exec('owned_local = 3')\n"
            "        assert locals()['owned_local'] == 3\n"
            "Child().call()\n"
            "assert os.system.__module__ == 'posix'\n"
            "assert subprocess.run.__module__ == 'subprocess'\n"
            "print(local_value)\n"
        )
        self.assertIsNone(result["error"])
        self.assertEqual(result["stdout"], "42\n")
        self.assertEqual(result["stderr"], "error is data\n")

    def test_alias_lookup_and_arguments_are_evaluated_once(self):
        result = self.cell(
            "import subprocess, sys\n"
            "lookups, arguments = [], []\n"
            "def select():\n"
            "    lookups.append(1)\n"
            "    return subprocess.run\n"
            "def command():\n"
            "    arguments.append(1)\n"
            "    return [sys.executable, '-c', 'raise SystemExit(6)']\n"
            "select()(*[command()], **{'capture_output': True})"
        )
        self.assertIn("CalledProcessError", result["error"] or "")
        checked = self.cell("assert lookups == [1] and arguments == [1]\nprint('once')")
        self.assertIsNone(checked["error"])
        self.assertEqual(checked["stdout"], "once\n")

    def test_functions_defined_in_prior_cells_retain_checking(self):
        self.assertIsNone(self.cell(
            "import os\n"
            "_synon_checked_process_call = 'user-owned'\n"
            "def compute():\n    os.system('exit 8')"
        )["error"])
        failed = self.cell("compute()")
        self.assertIn("CalledProcessError", failed["error"] or "")
        result = self.cell("assert _synon_checked_process_call == 'user-owned'\nprint('preserved')")
        self.assertIsNone(result["error"])
        self.assertEqual(result["stdout"], "preserved\n")
