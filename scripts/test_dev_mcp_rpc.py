"""Real-pipe tests for the development MCP client; no server binary is required."""

import contextlib
import io
import os
import sys
import time
import unittest

from dev_mcp_rpc import MAX_REQUESTS, MAX_STDERR_BYTES, PROTOCOL_VERSION, RPCClient, RPCError


FAKE_SERVER = r'''
import json
import os
import sys
import time

mode = sys.argv[1]
request = json.loads(sys.stdin.buffer.readline())
assert request["method"] == "initialize"
assert request["params"]["protocolVersion"] == "2024-11-05"
if mode == "handshake-timeout":
    sys.stderr.write("handshake stalled\n")
    sys.stderr.flush()
    time.sleep(60)
if mode == "handshake-malformed":
    print("{invalid", flush=True)
    time.sleep(60)
response = {"jsonrpc": "2.0", "id": request["id"], "result": {
    "protocolVersion": "wrong" if mode == "wrong-version" else "2024-11-05",
    "capabilities": {}, "serverInfo": {"name": "fake", "version": "1.0.0"},
}}
if mode == "boolean-id":
    response["id"] = True
print(json.dumps(response), flush=True)
notification = json.loads(sys.stdin.buffer.readline())
assert notification == {"jsonrpc": "2.0", "method": "notifications/initialized"}
for _ in range(200):
    line = sys.stdin.buffer.readline()
    if not line:
        break
    request = json.loads(line)
    method = request["method"]
    if method == "timeout":
        sys.stderr.write("D" * (256 * 1024))
        sys.stderr.flush()
        sys.stdout.write('{"jsonrpc":"2.0"')
        sys.stdout.flush()
        time.sleep(60)
    if method == "overflow":
        sys.stdout.buffer.write(b"x" * (16 * 1024 * 1024 + 1))
        sys.stdout.flush()
        time.sleep(60)
    if method == "stderr-overflow":
        sys.stderr.buffer.write(b"x" * (16 * 1024 * 1024 + 1))
        sys.stderr.flush()
        time.sleep(60)
    if method == "malformed":
        print("{invalid", flush=True)
        continue
    response = {"jsonrpc": "2.0", "id": request["id"], "result": {
        "initialized": True, "method": method, "params": request.get("params"),
    }}
    if method == "unknown-id":
        response["id"] += 1
    if method == "wrong-jsonrpc":
        response["jsonrpc"] = "1.0"
    if method == "error":
        response.pop("result")
        response["error"] = {"code": -32601, "message": "method unavailable"}
    if method == "notification":
        print(json.dumps({"jsonrpc": "2.0", "method": "notifications/message", "params": {}}), flush=True)
    print(json.dumps(response), flush=True)
    if method == "stop-reading":
        time.sleep(60)
'''


def fake_client(mode="normal", timeout=3):
    return RPCClient([sys.executable, "-u", "-c", FAKE_SERVER, mode], timeout=timeout)


class RPCClientTests(unittest.TestCase):
    def assert_reaped(self, client):
        process = client._process
        self.assertIsNotNone(process)
        self.assertIsNotNone(process.returncode)
        for stream in (process.stdin, process.stdout, process.stderr):
            self.assertTrue(stream.closed)
        with self.assertRaises(ChildProcessError):
            os.waitpid(process.pid, os.WNOHANG)

    def test_handshake_requests_notifications_and_clean_stdout(self):
        output = io.StringIO()
        client = fake_client()
        with contextlib.redirect_stdout(output), client as rpc:
            self.assertEqual(rpc.initialize_response["result"]["protocolVersion"], PROTOCOL_VERSION)
            envelope = rpc.call("standards_audit", {"config_path": ".standards.yaml"})
            self.assertEqual(envelope["id"], 2)
            self.assertTrue(envelope["result"]["initialized"])
            self.assertEqual(envelope["result"]["params"], {
                "name": "standards_audit", "arguments": {"config_path": ".standards.yaml"},
            })
            self.assertEqual(rpc.request("notification")["result"]["method"], "notification")
            self.assertEqual(rpc.request("error")["error"]["code"], -32601)
        self.assertEqual(output.getvalue(), "")
        self.assert_reaped(client)
        client.close()  # Cleanup is idempotent.

    def test_bad_handshakes_close_and_reap(self):
        for mode, text in (("handshake-malformed", "malformed JSON"),
                           ("wrong-version", "negotiate MCP protocol"),
                           ("boolean-id", "unknown id")):
            with self.subTest(mode=mode):
                client = fake_client(mode)
                with self.assertRaisesRegex(RPCError, text):
                    with client:
                        self.fail("invalid handshake entered the context")
                self.assert_reaped(client)

    def test_bad_responses_close_and_reap(self):
        for method, text in (("malformed", "malformed JSON"),
                             ("unknown-id", "unknown id"),
                             ("wrong-jsonrpc", "JSON-RPC 2.0")):
            with self.subTest(method=method):
                client = fake_client()
                with client as rpc:
                    with self.assertRaisesRegex(RPCError, text):
                        rpc.request(method)
                    self.assert_reaped(client)

    def test_timeout_interrupts_partial_pipe_read_and_bounds_stderr(self):
        client = fake_client()
        with client as rpc:
            rpc.timeout = 0.15
            started = time.monotonic()
            with self.assertRaisesRegex(RPCError, "timed out") as raised:
                rpc.request("timeout")
            self.assertLess(time.monotonic() - started, 2)
            self.assertLessEqual(len(rpc.stderr.encode()), MAX_STDERR_BYTES)
            self.assertIn("Server stderr (bounded)", str(raised.exception))
            self.assert_reaped(client)

    def test_handshake_timeout_also_cleans_up(self):
        client = fake_client("handshake-timeout", timeout=0.15)
        with self.assertRaisesRegex(RPCError, "timed out"):
            with client:
                self.fail("stalled handshake entered the context")
        self.assert_reaped(client)

    def test_response_overflow_closes_and_reaps(self):
        for method in ("overflow", "stderr-overflow"):
            with self.subTest(method=method):
                client = fake_client()
                with client as rpc:
                    with self.assertRaisesRegex(RPCError, "16 MiB byte limit"):
                        rpc.request(method)
                    self.assertLessEqual(len(rpc.stderr.encode()), MAX_STDERR_BYTES)
                    self.assert_reaped(client)

    def test_timeout_interrupts_a_blocked_request_write(self):
        client = fake_client()
        with client as rpc:
            rpc.request("stop-reading")
            rpc.timeout = 0.15
            started = time.monotonic()
            with self.assertRaisesRegex(RPCError, "timed out"):
                rpc.request("large", {"data": "x" * (2 * 1024 * 1024)})
            self.assertLess(time.monotonic() - started, 2)
            self.assert_reaped(client)

    def test_failed_start_does_not_leave_resources_open(self):
        client = RPCClient(["/nonexistent/praetor-dev-mcp-fixture"])
        with self.assertRaises(RPCError):
            with client:
                self.fail("missing executable entered the context")
        self.assertIsNone(client._process)
        self.assertTrue(client._closed)

    def test_request_limit_includes_initialize(self):
        client = fake_client()
        with client as rpc:
            for _ in range(MAX_REQUESTS - 1):
                response = rpc.request("echo")
            self.assertEqual(response["id"], MAX_REQUESTS)
            with self.assertRaisesRegex(RPCError, "request count exceeds"):
                rpc.request("one-too-many")
            self.assert_reaped(client)

    def test_invalid_configuration_does_not_start_process(self):
        for command, timeout in (([], 1), ("python -c pass", 1), (["python"], 0),
                                 (["python"], float("inf"))):
            with self.subTest(command=command, timeout=timeout):
                with self.assertRaises(ValueError):
                    RPCClient(command, timeout=timeout)


if __name__ == "__main__":
    unittest.main()
