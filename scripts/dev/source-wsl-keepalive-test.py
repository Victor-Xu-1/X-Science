#!/usr/bin/env python3
"""Owned-stdin contract; initialization is replaced, holding and timeout are real."""
import contextlib
import json
import os
from pathlib import Path
import pwd
import select
import signal
import subprocess
import tempfile
import time
import unittest

SCRIPT = Path(__file__).with_name("source-wsl-keepalive.sh")
START = b"XSCIENCE_KEEPALIVE_V1_START\n"
SUCCESS = r"""
if IFS= read -r unexpected; then
  return 91
fi
printf 'bootstrapped\n' >> "$KEEPALIVE_FIXTURE_ROOT/bootstrap-done"
"""


class KeeperStdinLifetimeTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="xscience-keeper-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.admin_log = self.root / "admin-called"
        guards = self.root / "bin"
        guards.mkdir()
        # Fail closed before any real service action, including on old source.
        # The admission RED is an unexpected call to this initialization boundary.
        for command in ("loginctl", "systemctl", "runuser"):
            guard = guards / command
            guard.write_text(
                '#!/usr/bin/env bash\n'
                'printf "%s\\n" "$0" >> "$KEEPALIVE_FIXTURE_ROOT/admin-called"\n'
                'exit 73\n',
                encoding="utf-8",
            )
            guard.chmod(0o700)
        self.environment = os.environ.copy()
        self.environment.update(
            PATH=str(guards) + os.pathsep + self.environment.get("PATH", os.defpath),
            KEEPALIVE_FIXTURE_ROOT=str(self.root),
            X_SCIENCE_DEV_WSL_USER=pwd.getpwuid(os.getuid()).pw_name,
        )

    @contextlib.contextmanager
    def keeper(self, bootstrap=SUCCESS):
        # Source the actual module and replace only its initialization boundary.
        # The production main still owns validation, GNU timeout, exec cat and EOF.
        driver = (
            'source "$1"\n'
            'source_dev_keepalive_bootstrap() {\n' + bootstrap + '\n}\n'
            'source_dev_keepalive_main\n'
        )
        process = subprocess.Popen(
            ["bash", "-c", driver, "keeper-fixture", str(SCRIPT)],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=self.environment,
            start_new_session=True,
        )
        try:
            yield process
        finally:
            if not process.stdin.closed:
                process.stdin.close()
            try:
                process.wait(timeout=3)
            except subprocess.TimeoutExpired:
                process.kill()  # This fixture's retained direct child only.
                process.wait(timeout=3)
            process.stdout.close()
            process.stderr.close()

    def send(self, process, data):
        process.stdin.write(data)
        process.stdin.flush()

    def wait_file(self, process, filename):
        path = self.root / filename
        deadline = time.monotonic() + 3
        while time.monotonic() < deadline:
            if path.exists():
                return path
            if process.poll() is not None:
                self.fail(
                    f"fixture exited before {filename}: "
                    f"rc={process.returncode}, stderr={process.stderr.read()!r}"
                )
            time.sleep(0.01)
        self.fail(f"fixture did not create {filename}")

    def wait_holding(self, process):
        self.wait_file(process, "bootstrap-done")
        deadline = time.monotonic() + 3
        while time.monotonic() < deadline and process.poll() is None:
            try:
                if Path(f"/proc/{process.pid}/comm").read_text().strip() == "cat":
                    return
            except FileNotFoundError:
                pass
            time.sleep(0.01)
        self.fail("the actual owner process did not exec foreground cat")

    @contextlib.contextmanager
    def owned_deadline_child(self, process):
        receipt = json.loads(self.wait_file(process, "deadline-child.json").read_text())
        descriptor = os.pidfd_open(receipt["pid"])
        identity_matches = False
        try:
            # Validate creation identity before using the fixture-owned pidfd.
            fields = Path(f"/proc/{receipt['pid']}/stat").read_text().split(") ", 1)[1].split()
            identity_matches = receipt["start_ticks"] == int(fields[19])
            self.assertTrue(identity_matches, "fixture child creation identity changed")
            self.assertFalse(
                receipt["inherited_term_ignored"],
                "bootstrap descendants inherited an ignored TERM disposition",
            )
            yield descriptor
        finally:
            if identity_matches and not select.select([descriptor], [], [], 0)[0]:
                signal.pidfd_send_signal(descriptor, signal.SIGKILL)
            os.close(descriptor)

    def test_rejects_unowned_start_before_initialization(self):
        for value in (
            b"",
            b"wrong\n",
            b"XSCIENCE_KEEPALIVE_V1_START\r\n",
            b"XSCIENCE_KEEPALIVE_V1_START extra\n",
            b"XSCIENCE_KEEPALIVE_V1_START",
            b"XSCIENCE_KEEPALIVE_V1_START\x00\n",
            b"\xef\xbb\xbfXSCIENCE_KEEPALIVE_V1_START\n",
        ):
            with self.subTest(first_line=value):
                self.admin_log.unlink(missing_ok=True)
                result = subprocess.run(
                    ["bash", str(SCRIPT)],
                    input=value,
                    capture_output=True,
                    env=self.environment,
                    timeout=3,
                )
                self.assertFalse(
                    self.admin_log.exists(),
                    "an unowned start entered initialization before admission",
                )
                self.assertNotEqual(0, result.returncode)

    def test_open_writer_and_extra_payload_are_drained_until_eof(self):
        with self.keeper() as process:
            self.send(process, START + b"extra payload\n" + START)
            self.wait_holding(process)
            time.sleep(0.15)
            self.assertIsNone(process.poll())
            self.send(process, b"later payload\n")
            process.stdin.close()
            self.assertEqual(0, process.wait(timeout=3))
            self.assertEqual(b"", process.stdout.read())
            self.assertEqual(["bootstrapped"], (self.root / "bootstrap-done").read_text().splitlines())
            self.assertFalse(self.admin_log.exists())

    def test_eof_after_valid_start_does_not_leave_a_holder(self):
        with self.keeper() as process:
            self.send(process, START + b"trailing data")
            process.stdin.close()
            self.assertEqual(0, process.wait(timeout=3))
            self.assertTrue((self.root / "bootstrap-done").exists())
            self.assertEqual(b"", process.stdout.read())

    def test_bootstrap_failure_never_enters_holding(self):
        with self.keeper("return 37") as process:
            self.send(process, START)
            self.assertEqual(37, process.wait(timeout=3))
            self.assertFalse((self.root / "bootstrap-done").exists())
            self.assertFalse(self.admin_log.exists())

    def test_bootstrap_deadline_preserves_unbounded_healthy_holding(self):
        child = self.root / "deadline-child.py"
        child.write_text(
            "import json,os,signal,sys\n"
            "from pathlib import Path\n"
            "inherited_term_ignored=signal.getsignal(signal.SIGTERM)==signal.SIG_IGN\n"
            "signal.signal(signal.SIGTERM, signal.SIG_IGN)\n"
            'fields=Path("/proc/self/stat").read_text().split(") ",1)[1].split()\n'
            'path=Path(sys.argv[1]); pending=path.with_suffix(".tmp")\n'
            'pending.write_text(json.dumps({"pid":os.getpid(),"start_ticks":int(fields[19]),"inherited_term_ignored":inherited_term_ignored}))\n'
            'pending.replace(path)\n'
            "while True: signal.pause()\n",
            encoding="utf-8",
        )
        blocked_bootstrap = r"""
python3 "$KEEPALIVE_FIXTURE_ROOT/deadline-child.py" "$KEEPALIVE_FIXTURE_ROOT/deadline-child.json" &
wait
"""
        with self.keeper(blocked_bootstrap) as blocked:
            started = time.monotonic()
            self.send(blocked, START)
            with self.owned_deadline_child(blocked) as descriptor:
                with self.keeper() as healthy:
                    self.send(healthy, START)
                    self.wait_holding(healthy)
                    holding_started = time.monotonic()
                    # The real production budget is 30s + at most 5s KILL grace.
                    # Only the child ignores TERM; the wrapper uses production signals.
                    code = blocked.wait(timeout=40)
                    elapsed = time.monotonic() - started
                    self.assertIn(code, (124, 137))
                    self.assertGreaterEqual(elapsed, 29)
                    self.assertLess(elapsed, 40)
                    self.assertTrue(
                        select.select([descriptor], [], [], 1)[0],
                        "bootstrap's TERM-resistant child survived the deadline",
                    )
                    self.assertGreaterEqual(time.monotonic() - holding_started, 30)
                    self.assertIsNone(healthy.poll(), "holding inherited the bootstrap lifetime timeout")
                    self.send(healthy, b"still owned\n")
                    healthy.stdin.close()
                    self.assertEqual(0, healthy.wait(timeout=3))
        self.assertFalse(self.admin_log.exists())


if __name__ == "__main__":
    unittest.main(verbosity=2)
