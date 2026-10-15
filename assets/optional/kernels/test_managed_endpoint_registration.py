"""Focused serialization checks without starting a kernel or model runtime."""

import ast
from pathlib import Path
import unittest


class ManagedEndpointRegistrationContract(unittest.TestCase):
    def setUp(self):
        path = Path(__file__).with_name("x_science_host_bridge.py")
        module = ast.parse(path.read_text(encoding="utf-8"))
        accessor = next(node for node in module.body
                        if isinstance(node, ast.ClassDef)
                        and node.name == "_ModelEndpointsAccessor")
        self.calls = []

        def rpc(method, args, kwargs):
            self.calls.append((method, args, kwargs))
            return {"registered": True}

        namespace = {"_host_rpc": rpc}
        exec(compile(ast.Module(body=[accessor], type_ignores=[]), str(path), "exec"), namespace)
        self.endpoints = namespace["_ModelEndpointsAccessor"]()

    def test_local_owner_is_forwarded_without_rewriting(self):
        owner = {"version": 1, "kind": "docker",
                 "docker": {"context": "default", "container": "private-fixture"},
                 "output": {"segmentBytes": 4096, "segments": 2}}
        self.endpoints.register("private-fixture", "http://127.0.0.1:25001", "runbook",
                                start="true", stop="true", live="/health", owner=owner)
        self.assertIs(self.calls[0][1][0]["owner"], owner)
        self.assertEqual(self.calls[0][0], "host.model_endpoints.register")

    def test_historical_local_call_does_not_invent_owner_bytes(self):
        self.endpoints.register("private-fixture", "http://127.0.0.1:25001", "runbook",
                                start="true", stop="true", live="/health")
        self.assertNotIn("owner", self.calls[0][1][0])

    def test_remote_rejects_local_owner_and_keeps_plain_https_contract(self):
        with self.assertRaises(TypeError):
            self.endpoints.register("remote", "https://example.invalid/v1", "runbook", owner={})
        self.assertEqual(self.calls, [])
        self.endpoints.register("remote", "https://example.invalid/v1", "runbook")
        self.assertEqual(self.calls[0][1][0], {"name": "remote", "url": "https://example.invalid/v1",
                                           "skill": "runbook", "credential": "NVIDIA_API_KEY"})


if __name__ == "__main__":
    unittest.main()
