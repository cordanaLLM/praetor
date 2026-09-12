"""Native protocol and exact-hash trust regression tests without model calls."""

import copy
from pathlib import Path
import sys
import unittest

from dev_codex_hooks import CodexClient, project_hooks, trust_hook
from dev_mcp_rpc import RPCError

ROOT = Path("/repo")
HASH = "sha256:" + "a" * 64
HOOK = {"key": "project:pre_tool_use:0:0", "sourcePath": "/repo/.codex/hooks.json",
        "enabled": True, "handlerType": "command", "currentHash": HASH,
        "trustStatus": "untrusted"}


class FakeClient:
    def __init__(self):
        self.hook = dict(HOOK)
        self.row = {"cwd": str(ROOT), "hooks": [self.hook], "warnings": [], "errors": []}
        self.writes = []
        self.readback = True

    def request(self, method, params):
        if method == "hooks/list":
            return {"result": {"data": [copy.deepcopy(self.row)]}}
        self.writes.append((method, params))
        if self.readback:
            self.hook["trustStatus"] = "trusted"
        return {"result": {}}


class HookTrust(unittest.TestCase):
    def test_only_exact_selected_hash_is_written(self):
        client = FakeClient()
        client.row["hooks"].append({"key": "user:other", "sourcePath": "/user/.codex/hooks.json"})
        result = trust_hook(client, ROOT, HOOK["key"], HASH)
        self.assertEqual(result["trustStatus"], "trusted")
        self.assertEqual(client.writes, [("config/batchWrite", {
            "edits": [{"keyPath": "hooks.state", "value": {HOOK["key"]: {"trusted_hash": HASH}},
                       "mergeStrategy": "upsert"}], "reloadUserConfig": True,
        })])

    def test_stale_missing_disabled_or_duplicate_hook_does_not_write(self):
        for mode in ("stale", "missing", "disabled", "duplicate", "foreign"):
            with self.subTest(mode=mode):
                client = FakeClient()
                if mode == "stale":
                    client.hook["currentHash"] = "sha256:" + "b" * 64
                elif mode == "missing":
                    client.row["hooks"] = []
                elif mode == "disabled":
                    client.hook["enabled"] = False
                elif mode == "duplicate":
                    client.row["hooks"].append(dict(client.hook))
                else:
                    client.hook["sourcePath"] = "/user/.codex/hooks.json"
                with self.assertRaises(RPCError):
                    trust_hook(client, ROOT, HOOK["key"], HASH)
                self.assertEqual(client.writes, [])

    def test_invalid_hash_and_discovery_errors_fail_closed(self):
        client = FakeClient()
        for value in ("", "a" * 64, HASH + "x"):
            with self.assertRaises(ValueError):
                trust_hook(client, ROOT, HOOK["key"], value)
        for field, value in (("warnings", ["stale"]), ("errors", ["bad file"]),
                             ("cwd", "/other"), ("hooks", [None]),
                             ("hooks", [{}] * 1025)):
            broken = FakeClient()
            broken.row[field] = value
            with self.assertRaises(RPCError):
                project_hooks(broken, ROOT)
        self.assertEqual(client.writes, [])

    def test_failed_readback_is_not_success(self):
        client = FakeClient()
        client.readback = False
        with self.assertRaisesRegex(RPCError, "readback failed"):
            trust_hook(client, ROOT, HOOK["key"], HASH)

    def test_real_pipe_native_handshake(self):
        server = '''
import json,sys
request=json.loads(sys.stdin.readline())
assert request['method']=='initialize'
assert request['params']['capabilities']['experimentalApi'] is True
print(json.dumps({'id':request['id'],'result':{'userAgent':'codex/0.145.0'}}),flush=True)
assert json.loads(sys.stdin.readline())=={'method':'initialized'}
request=json.loads(sys.stdin.readline())
assert request['method']=='hooks/list'
print(json.dumps({'id':request['id'],'result':{'data':[{
 'cwd':'/repo','hooks':[],'warnings':[],'errors':[]}]}}),flush=True)
'''
        with CodexClient([sys.executable, "-c", server], timeout=3) as client:
            self.assertEqual(project_hooks(client, ROOT), [])


if __name__ == "__main__":
    unittest.main(verbosity=2)
