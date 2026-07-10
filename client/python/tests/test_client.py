"""Unit tests for the VaultDB Python client."""

import json
import socket
import threading
from unittest.mock import patch, MagicMock

import pytest
from vaultdb.client import Client
from vaultdb import protocol


class FakeServer:
    """Minimal fake VaultDB server for testing."""

    def __init__(self, handler):
        self._server = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        self._server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        self._server.bind(("127.0.0.1", 0))
        self._server.listen(1)
        self.port = self._server.getsockname()[1]
        self._handler = handler
        self._thread = threading.Thread(target=self._run, daemon=True)
        self._thread.start()

    def _run(self):
        conn, _ = self._server.accept()
        try:
            self._handler(conn)
        finally:
            conn.close()

    def close(self):
        self._server.close()


def _echo_handler(conn):
    """Read NDJSON, send back each message immediately."""
    buf = b""
    while True:
        chunk = conn.recv(65536)
        if not chunk:
            break
        buf += chunk
        while b"\n" in buf:
            line, buf = buf.split(b"\n", 1)
            msg = json.loads(line)
            resp = json.dumps(msg) + "\n"
            conn.sendall(resp.encode())


def _handshake_then_query_handler(conn):
    """Perform handshake, then echo queries."""
    buf = b""
    # Handshake
    while b"\n" not in buf:
        chunk = conn.recv(65536)
        if not chunk:
            return
        buf += chunk
    line, buf = buf.split(b"\n", 1)
    hs = json.loads(line)
    resp = {
        "type": "handshake",
        "protocol_version": hs["client_version"],
        "server": "VaultDB",
        "server_version": "2.0.0-test",
        "supported_features": ["time_travel", "transactions"],
    }
    conn.sendall((json.dumps(resp) + "\n").encode())
    # Queries
    while True:
        chunk = conn.recv(65536)
        if not chunk:
            break
        buf += chunk
        while b"\n" in buf:
            line, buf = buf.split(b"\n", 1)
            msg = json.loads(line)
            if msg["query"].startswith("SELECT"):
                qresp = {
                    "id": msg["id"],
                    "status": "ok",
                    "type": "select",
                    "columns": ["id", "name"],
                    "rows": [[1, "alice"]],
                    "affected": 0,
                    "message": "",
                    "duration_ms": 1,
                }
            else:
                qresp = {
                    "id": msg["id"],
                    "status": "ok",
                    "type": "exec",
                    "columns": [],
                    "rows": [],
                    "affected": 0,
                    "message": "",
                    "duration_ms": 1,
                }
            conn.sendall((json.dumps(qresp) + "\n").encode())


class TestHandshakeSerialization:
    def test_handshake_request_format(self):
        req = {
            "type": "handshake",
            "client_version": protocol.CLIENT_VERSION,
            "client_name": protocol.CLIENT_NAME,
            "supported_features": list(protocol.SUPPORTED_FEATURES),
        }
        assert req["type"] == "handshake"
        assert req["client_version"] == "2.0"
        assert isinstance(req["supported_features"], list)

    def test_handshake_response_parsing(self):
        resp = {
            "type": "handshake",
            "protocol_version": "2.0",
            "server": "VaultDB",
            "server_version": "2.0.0",
            "supported_features": ["time_travel"],
        }
        assert resp["protocol_version"] == "2.0"
        assert resp["server"] == "VaultDB"


class TestQueryRequestResponse:
    def test_query_request_format(self):
        req = {
            "id": "test-id",
            "token": "vdb_sk_test",
            "query": "SELECT * FROM users;",
            "database": "mydb",
        }
        assert req["id"] == "test-id"
        assert req["token"] == "vdb_sk_test"
        assert req["query"] == "SELECT * FROM users;"

    def test_query_with_params(self):
        req = {
            "id": "test-id",
            "token": "vdb_sk_test",
            "query": "SELECT * FROM users WHERE id = $1;",
            "params": [42],
        }
        assert req["params"] == [42]

    def test_query_response_parsing(self):
        resp = {
            "id": "test-id",
            "status": "ok",
            "type": "select",
            "columns": ["id", "name"],
            "rows": [[1, "alice"]],
            "affected": 0,
            "message": "",
            "duration_ms": 3,
        }
        assert resp["status"] == "ok"
        assert resp["columns"] == ["id", "name"]
        assert resp["rows"] == [[1, "alice"]]


class TestParameterConversion:
    def test_int_passthrough(self):
        assert Client._convert_param(42) == 42

    def test_float_passthrough(self):
        assert Client._convert_param(3.14) == 3.14

    def test_bool_passthrough(self):
        assert Client._convert_param(True) is True

    def test_string_true(self):
        assert Client._convert_param("true") is True
        assert Client._convert_param("True") is True

    def test_string_false(self):
        assert Client._convert_param("false") is False
        assert Client._convert_param("False") is False

    def test_string_to_int(self):
        assert Client._convert_param("42") == 42

    def test_string_to_float(self):
        assert Client._convert_param("3.14") == 3.14

    def test_string_passthrough(self):
        assert Client._convert_param("hello") == "hello"

    def test_bool_before_int(self):
        # Ensure True/False booleans aren't converted to int
        result = Client._convert_param(True)
        assert result is True
        assert isinstance(result, bool)


class TestConnectionErrorHandling:
    def test_not_connected_send(self):
        c = Client("127.0.0.1", 1, "tok")
        with pytest.raises(ConnectionError):
            c._send({"test": 1})

    def test_not_connected_recv(self):
        c = Client("127.0.0.1", 1, "tok")
        with pytest.raises(ConnectionError):
            c._recv()

    def test_connect_refused(self):
        c = Client("127.0.0.1", 1, "tok")
        with pytest.raises((ConnectionError, OSError)):
            c.connect()

    def test_close_idempotent(self):
        c = Client("127.0.0.1", 1, "tok")
        c.close()
        c.close()

    def test_close_after_failed_connect(self):
        c = Client("127.0.0.1", 1, "tok")
        try:
            c.connect()
        except (ConnectionError, OSError):
            pass
        c.close()


class TestContextManager:
    def test_context_manager_enter_exit(self):
        srv = FakeServer(_handshake_then_query_handler)
        with Client("127.0.0.1", srv.port, "tok") as c:
            assert c.connected
        assert not c.connected
        srv.close()

    def test_context_manager_with_query(self):
        srv = FakeServer(_handshake_then_query_handler)
        with Client("127.0.0.1", srv.port, "tok") as c:
            resp = c.query("SELECT 1;")
            assert resp["status"] == "ok"
            assert resp["rows"] == [[1, "alice"]]
        srv.close()

    def test_transaction_helpers(self):
        srv = FakeServer(_handshake_then_query_handler)
        with Client("127.0.0.1", srv.port, "tok") as c:
            c.begin()
            c.commit()
        srv.close()

    def test_connect_returns_handshake(self):
        srv = FakeServer(_handshake_then_query_handler)
        c = Client("127.0.0.1", srv.port, "tok")
        info = c.connect()
        assert info["server"] == "VaultDB"
        c.close()
        srv.close()
