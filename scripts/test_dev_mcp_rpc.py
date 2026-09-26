"""Real-pipe tests for the development MCP client; no server binary is required."""

import contextlib
import io
import os
from pathlib import Path
import signal
import sys
import tempfile
import time
import unittest
from unittest import mock

import dev_process
from dev_mcp_rpc import MAX_REQUESTS, MAX_STDERR_BYTES, PROTOCOL_VERSION, RPCClient, RPCError


# A command the way git runs one: it holds a lock, removes it on SIGTERM and leaves `cleaned`
# as evidence, and reports its pid in `command-ready`.
LOCK_HOLDING_COMMAND = r'''
import os, signal, sys, time
from pathlib import Path
root = Path(sys.argv[1])
def cleanup(number, _frame):
    (root / "lock").unlink(missing_ok=True)
    (root / "cleaned").touch()
    raise SystemExit(128 + number)
signal.signal(signal.SIGTERM, cleanup)
(root / "lock").touch()
(root / "command-ready.tmp").write_text(str(os.getpid()))
(root / "command-ready.tmp").rename(root / "command-ready")
time.sleep(30)
'''

FAKE_SERVER = r'''
import json
import os
import signal
import subprocess
import sys
import time

mode = sys.argv[1]
if mode == "ignore-term":
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
if mode == "forwarding":
    # praetor's shape: the command runs in a process group of its own, and the signal that
    # stops the server is forwarded to that group before the server exits.
    root = sys.argv[2]
    command = subprocess.Popen([sys.executable, "-c", sys.argv[3], root], start_new_session=True)
    def forward(number, _frame):
        os.killpg(command.pid, number)
        command.wait(timeout=5)
        os._exit(128 + number)
    signal.signal(signal.SIGTERM, forward)
    for _ in range(400):
        if os.path.exists(os.path.join(root, "command-ready")):
            break
        time.sleep(0.025)
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
if mode == "exit":
    sys.exit(0)
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


def fake_client(mode="normal", timeout=3, *args):
    return RPCClient([sys.executable, "-u", "-c", FAKE_SERVER, mode, *args], timeout=timeout)


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

    def test_close_lets_the_server_stop_its_commands(self):
        # praetor's server runs git and go commands in process groups of their own. SIGKILL on
        # the server's group ended the server and left those commands running, locks held.
        with tempfile.TemporaryDirectory(prefix="praetor-rpc-stop-") as temp:
            root = Path(temp)
            client = fake_client("forwarding", 3, temp, LOCK_HOLDING_COMMAND)
            with client:
                command = int((root / "command-ready").read_text())
            self.assert_reaped(client)
            try:
                self.assertTrue((root / "cleaned").exists(), "the command never got the signal")
                self.assertFalse((root / "lock").exists())
            finally:
                with contextlib.suppress(ProcessLookupError):
                    os.killpg(command, signal.SIGKILL)
                    self.fail("the command outlived the server")
            self.assertEqual(client._process.returncode, 128 + signal.SIGTERM)

    def test_close_kills_a_server_that_ignores_sigterm(self):
        with mock.patch.object(dev_process, "STOP_GRACE", 0.3):
            client = fake_client("ignore-term")
            with client:
                started = time.monotonic()
            self.assertGreaterEqual(time.monotonic() - started, 0.3)
        self.assert_reaped(client)
        self.assertEqual(client._process.returncode, -signal.SIGKILL)

    def test_close_after_the_server_exited(self):
        client = fake_client("exit")
        with client:
            client._process.wait(timeout=5)
        self.assert_reaped(client)
        self.assertEqual(client._process.returncode, 0)

    def test_invalid_configuration_does_not_start_process(self):
        for command, timeout in (([], 1), ("python -c pass", 1), (["python"], 0),
                                 (["python"], float("inf"))):
            with self.subTest(command=command, timeout=timeout):
                with self.assertRaises(ValueError):
                    RPCClient(command, timeout=timeout)


if __name__ == "__main__":
    unittest.main()
