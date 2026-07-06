"""VaultDB protocol v2 TCP client."""

import json
import socket
import threading
import uuid
from typing import Any, Optional

from . import protocol


class Client:
    def __init__(self, host: str = "localhost", port: int = 5432, token: str = ""):
        self.host = host
        self.port = port
        self.token = token
        self._conn: Optional[socket.socket] = None
        self._lock = threading.Lock()
        self._protocol_version: str = ""
        self._server_version: str = ""

    @property
    def connected(self) -> bool:
        return self._conn is not None

    def connect(self) -> dict:
        """Connect to server and perform handshake. Returns server info dict."""
        self._conn = socket.create_connection((self.host, self.port), timeout=10)
        self._conn.settimeout(300)
        return self._handshake()

    def _handshake(self) -> dict:
        hs_req = {
            "type": "handshake",
            "client_version": protocol.CLIENT_VERSION,
            "client_name": protocol.CLIENT_NAME,
            "supported_features": list(protocol.SUPPORTED_FEATURES),
        }
        self._send(hs_req)
        hs_resp = self._recv()
        self._protocol_version = hs_resp.get("protocol_version", "")
        self._server_version = hs_resp.get("server_version", "")
        return hs_resp

    def query(self, sql: str, params: Optional[list] = None, database: Optional[str] = None) -> dict:
        """Execute a query and return the response dict."""
        with self._lock:
            req: dict[str, Any] = {
                "id": str(uuid.uuid4()),
                "token": self.token,
                "query": sql,
            }
            if database is not None:
                req["database"] = database
            if params is not None:
                req["params"] = [self._convert_param(p) for p in params]
            self._send(req)
            return self._recv()

    def begin(self) -> dict:
        return self.query("BEGIN;")

    def commit(self) -> dict:
        return self.query("COMMIT;")

    def rollback(self) -> dict:
        return self.query("ROLLBACK;")

    def close(self):
        if self._conn:
            try:
                self._conn.close()
            except OSError:
                pass
            self._conn = None

    def __enter__(self):
        self.connect()
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        self.close()
        return False

    @staticmethod
    def _convert_param(val: Any) -> Any:
        """Convert string parameters to appropriate types for the protocol."""
        if isinstance(val, bool):
            return val
        if isinstance(val, (int, float)):
            return val
        if isinstance(val, str):
            if val.lower() == "true":
                return True
            if val.lower() == "false":
                return False
            try:
                return int(val)
            except ValueError:
                pass
            try:
                return float(val)
            except ValueError:
                pass
        return str(val)

    def _send(self, msg: dict):
        if self._conn is None:
            raise ConnectionError("not connected")
        data = json.dumps(msg) + "\n"
        self._conn.sendall(data.encode())

    def _recv(self) -> dict:
        if self._conn is None:
            raise ConnectionError("not connected")
        buf = b""
        while True:
            chunk = self._conn.recv(65536)
            if not chunk:
                raise ConnectionError("connection closed by server")
            buf += chunk
            if b"\n" in buf:
                line, _ = buf.split(b"\n", 1)
                return json.loads(line)
