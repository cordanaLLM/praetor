"""A bounded, synchronous JSON-RPC client for the development MCP stdio server."""

import json
import math
import os
import selectors
import subprocess
import time

from dev_process import stop_process_group


PROTOCOL_VERSION = "2024-11-05"
MAX_RESPONSE_BYTES = 16 * 1024 * 1024
MAX_REQUESTS = 100
MAX_STDERR_BYTES = 64 * 1024
READ_CHUNK_BYTES = 64 * 1024
CLEANUP_TIMEOUT = 5
# One-byte reads/writes are the worst case within the two byte ceilings. The
# allowance covers EOF/control events; selectors still block until I/O or timeout.
MAX_IO_POLLS = 2 * MAX_RESPONSE_BYTES + 8
MAX_RESPONSE_MESSAGES = MAX_RESPONSE_BYTES  # Every framed message consumes a newline.


class RPCError(RuntimeError):
    """The child failed the bounded MCP transport or protocol contract."""


def _reject_constant(value):
    raise ValueError(f"invalid JSON constant {value}")


class RPCClient:
    """Run command without a shell and perform the MCP initialization handshake.

    Responses, including valid JSON-RPC error envelopes, are returned unchanged.
    Transport/protocol failures close and reap the child. Use as a context manager;
    instances are single-use and requests are synchronous. The request ceiling
    includes initialize. Server notifications are validated and discarded.
    """

    def __init__(self, command: list[str], timeout: float = 30):
        if not command or not all(isinstance(arg, (str, os.PathLike)) for arg in command):
            raise ValueError("command must be a nonempty argument list")
        if isinstance(command, (str, bytes)):
            raise ValueError("command must be an argument list, not a shell command")
        if not math.isfinite(timeout) or timeout <= 0:
            raise ValueError("timeout must be finite and positive")
        self.command = tuple(os.fspath(arg) for arg in command)
        self.timeout = timeout
        self.initialize_response = None
        self._process = None
        self._selector = None
        self._stdout = bytearray()
        self._stderr = bytearray()
        self._request_count = 0
        self._closed = False

    @property
    def stderr(self):
        """Return at most 64 KiB of the server's most recent diagnostic output."""
        return self._stderr.decode("utf-8", errors="replace")

    def __enter__(self):
        if self._process is not None or self._closed:
            raise RPCError("RPCClient instances cannot be reused")
        try:
            self._start()
            self.initialize_response = self.request("initialize", self._initialize_params())
            self._validate_initialize()
            self._exchange(self._initialized_notification(), None)
            return self
        except BaseException as error:
            self._abort(error)

    def __exit__(self, _type, _value, _traceback):
        self.close()
        return False

    def _initialize_params(self):
        return {"protocolVersion": PROTOCOL_VERSION, "capabilities": {},
                "clientInfo": {"name": "praetor-dev-mcp", "version": "1.0.0"}}

    def _initialized_notification(self):
        return {"jsonrpc": "2.0", "method": "notifications/initialized"}

    @staticmethod
    def _validate_envelope(message):
        if not isinstance(message, dict) or message.get("jsonrpc") != "2.0":
            raise RPCError("server emitted an invalid JSON-RPC 2.0 envelope")

    def _start(self):
        self._process = subprocess.Popen(
            self.command, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
            stderr=subprocess.PIPE, bufsize=0, start_new_session=True,
        )
        self._selector = selectors.DefaultSelector()
        for name in ("stdin", "stdout", "stderr"):
            stream = getattr(self._process, name)
            os.set_blocking(stream.fileno(), False)
            if name != "stdin":
                self._selector.register(stream, selectors.EVENT_READ, name)

    def _validate_initialize(self):
        if "error" in self.initialize_response:
            raise RPCError(f"initialize failed: {self.initialize_response['error']}")
        result = self.initialize_response.get("result")
        if not isinstance(result, dict) or result.get("protocolVersion") != PROTOCOL_VERSION:
            raise RPCError("initialize did not negotiate MCP protocol " + PROTOCOL_VERSION)
        if not isinstance(result.get("capabilities"), dict):
            raise RPCError("initialize response has no capabilities object")
        info = result.get("serverInfo")
        if not isinstance(info, dict) or not all(isinstance(info.get(k), str) and info[k]
                                              for k in ("name", "version")):
            raise RPCError("initialize response has invalid serverInfo")

    def request(self, method, params=None):
        """Return one raw response within a monotonic deadline and byte ceiling."""
        try:
            if self._process is None or self._closed:
                raise RPCError("RPCClient is not running")
            if not isinstance(method, str) or not method:
                raise ValueError("request method must be a nonempty string")
            if self._request_count >= MAX_REQUESTS:
                raise RPCError(f"request count exceeds maximum of {MAX_REQUESTS}")
            self._request_count += 1
            message = {"jsonrpc": "2.0", "id": self._request_count, "method": method}
            if params is not None:
                message["params"] = params
            return self._exchange(message, self._request_count)
        except BaseException as error:
            self._abort(error)

    def call(self, name, args=None):
        """Call an MCP tool, retaining both transport and tool-result envelopes."""
        return self.request("tools/call", {"name": name, "arguments": {} if args is None else args})

    def _exchange(self, message, request_id):
        deadline = time.monotonic() + self.timeout
        payload = json.dumps(message, separators=(",", ":"), allow_nan=False).encode() + b"\n"
        if len(payload) > MAX_RESPONSE_BYTES:
            raise RPCError("request exceeds the 16 MiB byte limit")
        pending = memoryview(payload)
        received = len(self._stdout)
        self._selector.register(self._process.stdin, selectors.EVENT_WRITE, "stdin")
        for _ in range(MAX_IO_POLLS):
            if time.monotonic() >= deadline:
                break
            if not pending:
                if request_id is None:
                    return None
                response = self._next_response(request_id, deadline)
                if response is not None:
                    return response
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                break
            for key, _events in self._selector.select(remaining):
                if key.data == "stdin":
                    pending = self._write_pending(key.fileobj, pending)
                else:
                    received += self._read_ready(key.fileobj, key.data)
                    if received > MAX_RESPONSE_BYTES:
                        raise RPCError("server output exceeds the 16 MiB byte limit")
        else:
            raise RPCError("MCP exchange exceeded the I/O poll bound")
        raise RPCError(f"MCP request timed out after {self.timeout:g} seconds")

    def _write_pending(self, stream, pending):
        try:
            written = os.write(stream.fileno(), pending)
        except BlockingIOError:
            return pending
        if written <= 0:
            raise RPCError("server stdin closed before request was written")
        pending = pending[written:]
        if not pending:
            self._selector.unregister(stream)
        return pending

    def _read_ready(self, stream, name):
        try:
            chunk = os.read(stream.fileno(), READ_CHUNK_BYTES)
        except BlockingIOError:
            return 0
        if not chunk:
            self._selector.unregister(stream)
            if name == "stdout":
                # Parse a fully buffered response before treating clean exit as EOF.
                if b"\n" not in self._stdout:
                    raise RPCError("server stdout closed before a complete response")
            return 0
        if name == "stderr":
            self._stderr.extend(chunk)
            del self._stderr[:-MAX_STDERR_BYTES]
            return len(chunk)
        self._stdout.extend(chunk)
        return len(chunk)

    def _next_response(self, request_id, deadline):
        for _ in range(MAX_RESPONSE_MESSAGES):
            if b"\n" not in self._stdout:
                return None
            if time.monotonic() >= deadline:
                raise RPCError(f"MCP request timed out after {self.timeout:g} seconds")
            line, _, rest = self._stdout.partition(b"\n")
            self._stdout = bytearray(rest)
            try:
                message = json.loads(line.decode("utf-8"), parse_constant=_reject_constant)
            except (ValueError, RecursionError) as error:
                raise RPCError(f"server emitted malformed JSON: {error}") from error
            self._validate_envelope(message)
            if "id" not in message and self._is_notification(message):
                continue
            if type(message.get("id")) is not int or message["id"] != request_id:
                raise RPCError(f"server response has unknown id {message.get('id')!r}; expected {request_id}")
            self._validate_response(message)
            return message
        raise RPCError("MCP response exceeded the message count bound")

    @staticmethod
    def _is_notification(message):
        return (isinstance(message.get("method"), str) and bool(message["method"])
                and "result" not in message and "error" not in message
                and ("params" not in message or isinstance(message["params"], (dict, list))))

    @staticmethod
    def _validate_response(message):
        if "method" in message or ("result" in message) == ("error" in message):
            raise RPCError("response must contain exactly one of result or error")
        if "error" in message:
            error = message["error"]
            if (not isinstance(error, dict) or type(error.get("code")) is not int
                    or not isinstance(error.get("message"), str)):
                raise RPCError("response has an invalid JSON-RPC error object")

    def _abort(self, error):
        if self._closed:
            raise error
        self.close()
        if isinstance(error, (KeyboardInterrupt, SystemExit)):
            raise error
        diagnostic = "\nServer stderr (bounded):\n" + self.stderr if self._stderr else ""
        raise RPCError(str(error) + diagnostic) from error

    def close(self):
        """Stop the isolated process group, reap the server, and close every pipe.

        The server gets SIGTERM and time to stop the commands it runs in process groups of
        their own before the group is killed (dev_process.stop_process_group).
        """
        if self._closed:
            return
        self._closed = True
        process = self._process
        try:
            if process is not None:
                stop_process_group(process)
                process.wait(timeout=CLEANUP_TIMEOUT)
                self._drain_stderr()
        finally:
            if self._selector is not None:
                self._selector.close()
            if process is not None:
                for name in ("stdin", "stdout", "stderr"):
                    stream = getattr(process, name)
                    if stream is not None:
                        stream.close()

    def _drain_stderr(self):
        if self._process.stderr is None:
            return
        for _ in range(MAX_STDERR_BYTES // READ_CHUNK_BYTES + 1):
            try:
                chunk = os.read(self._process.stderr.fileno(), READ_CHUNK_BYTES)
            except BlockingIOError:
                break
            if not chunk:
                break
            self._stderr.extend(chunk)
            del self._stderr[:-MAX_STDERR_BYTES]
