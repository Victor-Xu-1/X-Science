"""Native transport fault tests; no cloud or scientific work."""
import hashlib
import importlib
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
import time

sys.path.insert(0, sys.argv.pop(1))
sdk = importlib.import_module("operon_compute_provider")
transfer = importlib.import_module("operon_compute_provider.transfer")


class Process:
    def __init__(self, command, fail):
        self.child = subprocess.Popen(command, stdout=subprocess.PIPE)
        self.fail = fail

    @property
    def stdout(self):
        try:
            while data := self.child.stdout.read(8192):
                yield data
                if self.fail:
                    raise OSError("controlled disconnect")
        finally:
            if self.child.poll() is None:
                self.child.terminate()
            self.child.wait()
            self.child.stdout.close()

    def wait(self):
        return self.child.wait()


class Provider:
    def __init__(self):
        self.disconnect, self.commands = True, []

    def exec(self, sid, command, stdin=None):
        assert sid == "owned-fixture" and stdin is None
        self.commands.append(command[-1])
        fail = self.disconnect and "tail -c" in command[-1]
        if fail:
            self.disconnect = False
        return Process(command, fail)


class ProtocolTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.stage = self.root / "stage"
        self.stage.mkdir(mode=0o700)
        transfer.WORK = str(self.root / "remote")
        remote = Path(transfer.WORK) / ".synon-harvest"
        remote.mkdir(parents=True)
        self.name = "archive-" + "a" * 64 + "-" + "b" * 64 + ".tar.gz"
        self.remote = remote / self.name
        (Path(transfer.WORK) / "_synon_harvest.sh").write_bytes((Path(transfer.__file__).parent.parent / "harvest.sh.tmpl").read_bytes())
        self.body = b"controlled bytes\n" * 10000
        self.remote.write_bytes(self.body)
        self.sha = hashlib.sha256(self.body).hexdigest()

    def download(self, provider):
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            result = transfer._download(provider, "owned-fixture", str(self.stage), self.name, self.sha, len(self.body))
            if result["ready"]:
                return result
            time.sleep(0.01)
        self.fail("native prefix verification did not finish")

    def test_resume(self):
        provider = Provider()
        with self.assertRaises(OSError):
            self.download(provider)
        self.assertEqual((self.stage / "out.tar.gz.partial").stat().st_size, 8192)
        self.assertTrue(self.download(provider)["ready"])
        self.assertEqual((self.stage / "out.tar.gz").read_bytes(), self.body)
        self.assertTrue(any("tail -c +8193" in command for command in provider.commands))
        self.assertFalse(any("run.sh" in command for command in provider.commands))

    def test_changed_prefix_is_retained_not_silently_restarted(self):
        provider = Provider()
        with self.assertRaises(OSError):
            self.download(provider)
        self.remote.write_bytes(b"X" + self.body[1:])
        with self.assertRaises(sdk.ByocError):
            self.download(provider)
        self.assertEqual((self.stage / "out.tar.gz.partial").stat().st_size, 8192)

    def test_host_stage_binding(self):
        resident = sdk.ByocResident.__new__(sdk.ByocResident)
        resident._bound_stage = str(self.stage)
        self.assertEqual(resident._stage({"stage": str(self.stage)}), str(self.stage))
        with self.assertRaises(sdk.ByocError):
            resident._stage({"stage": str(self.root)})

    def test_partial_symlink(self):
        outside = self.root / "private"
        outside.write_bytes(b"untouched")
        (self.stage / "out.tar.gz.partial").symlink_to(outside)
        with self.assertRaises(sdk.ByocError):
            self.download(Provider())
        self.assertEqual(outside.read_bytes(), b"untouched")


if __name__ == "__main__":
    unittest.main()
