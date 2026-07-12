#!/usr/bin/env python3
"""DocVault — Web Interface Server"""
import sys
import os
import json
from http.server import HTTPServer, SimpleHTTPRequestHandler
from urllib.parse import urlparse, parse_qs

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "client", "python"))
from vaultdb import Client

DB_HOST = "localhost"
DB_PORT = 5454
DB_NAME = "docvault"
STATIC_DIR = os.path.join(os.path.dirname(__file__), "static")


def get_db():
    c = Client(DB_HOST, DB_PORT)
    c.connect()
    c.query(f"USE {DB_NAME};")
    return c


def query_db(sql):
    c = get_db()
    try:
        return c.query(sql)
    except Exception as e:
        return {"status": "error", "message": str(e)}
    finally:
        try:
            c.close()
        except Exception:
            pass


class DocVaultHandler(SimpleHTTPRequestHandler):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, directory=STATIC_DIR, **kwargs)

    def do_GET(self):
        try:
            parsed = urlparse(self.path)
            if parsed.path == "/api/documents":
                self.handle_list_documents()
            elif parsed.path == "/api/document":
                params = parse_qs(parsed.query)
                doc_id = params.get("id", [None])[0]
                self.handle_get_document(doc_id)
            elif parsed.path == "/api/versions":
                params = parse_qs(parsed.query)
                doc_id = params.get("doc_id", [None])[0]
                self.handle_list_versions(doc_id)
            elif parsed.path == "/api/stats":
                self.handle_stats()
            elif parsed.path == "/api/audit":
                self.handle_audit()
            else:
                super().do_GET()
        except Exception as e:
            try:
                self.send_json({"error": str(e)}, 500)
            except Exception:
                pass

    def do_POST(self):
        try:
            parsed = urlparse(self.path)
            content_length = int(self.headers.get("Content-Length", 0))
            body = json.loads(self.rfile.read(content_length)) if content_length > 0 else {}

            if parsed.path == "/api/document":
                self.handle_create_document(body)
            elif parsed.path == "/api/document/update":
                self.handle_update_document(body)
            elif parsed.path == "/api/document/delete":
                self.handle_delete_document(body)
            elif parsed.path == "/api/search":
                self.handle_search(body)
            else:
                self.send_error(404)
        except Exception as e:
            try:
                self.send_json({"error": str(e)}, 500)
            except Exception:
                pass

    def send_json(self, data, status=200):
        try:
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Access-Control-Allow-Origin", "*")
            self.end_headers()
            self.wfile.write(json.dumps(data, ensure_ascii=False).encode())
        except Exception:
            pass  # Client disconnected

    def handle_list_documents(self):
        result = query_db(
            "SELECT id, doc_number, title, created_at, status, department, author, file_size "
            "FROM documents ORDER BY created_at DESC;"
        )
        docs = []
        for row in result.get("rows", []):
            docs.append({
                "id": int(row[0]), "doc_number": row[1], "title": row[2],
                "created_at": row[3], "status": row[4], "department": row[5],
                "author": row[6], "file_size": int(row[7]) if row[7] else 0
            })
        self.send_json({"documents": docs})

    def handle_get_document(self, doc_id):
        if not doc_id:
            self.send_json({"error": "id required"}, 400)
            return
        result = query_db(
            f"SELECT id, doc_number, title, content, created_at, updated_at, "
            f"status, department, author, file_size FROM documents WHERE id = {doc_id};"
        )
        rows = result.get("rows", [])
        if not rows:
            self.send_json({"error": "not found"}, 404)
            return
        row = rows[0]
        self.send_json({"document": {
            "id": int(row[0]), "doc_number": row[1], "title": row[2],
            "content": row[3], "created_at": row[4], "updated_at": row[5],
            "status": row[6], "department": row[7], "author": row[8],
            "file_size": int(row[9]) if row[9] else 0
        }})

    def handle_create_document(self, body):
        doc_number = body.get("doc_number", "")
        title = body.get("title", "")
        content = body.get("content", "")
        department = body.get("department", "")
        author = body.get("author", "")
        file_size = body.get("file_size", 0)

        if not doc_number or not title:
            self.send_json({"error": "doc_number and title required"}, 400)
            return

        result = query_db("SELECT MAX(id) FROM documents;")
        max_id = int(result.get("rows", [["0"]])[0][0]) or 0
        new_id = max_id + 1

        sql = (
            f"INSERT INTO documents (id, doc_number, title, content, created_at, "
            f"status, department, author, file_size) VALUES "
            f"({new_id}, '{doc_number}', '{title}', '{content}', "
            f"CURRENT_TIMESTAMP, 'active', '{department}', '{author}', {file_size});"
        )
        result = query_db(sql)
        if result.get("status") == "ok":
            self.send_json({"id": new_id, "status": "created"})
        else:
            self.send_json({"error": result.get("message", "create failed")}, 500)

    def handle_update_document(self, body):
        doc_id = body.get("id")
        if not doc_id:
            self.send_json({"error": "id required"}, 400)
            return
        fields = []
        for key in ["title", "content", "status", "department", "author"]:
            if key in body:
                fields.append(f"{key} = '{body[key]}'")
        if not fields:
            self.send_json({"error": "no fields to update"}, 400)
            return
        sql = f"UPDATE documents SET {', '.join(fields)} WHERE id = {doc_id};"
        result = query_db(sql)
        self.send_json({"status": "updated" if result.get("status") == "ok" else "error"})

    def handle_delete_document(self, body):
        doc_id = body.get("id")
        if not doc_id:
            self.send_json({"error": "id required"}, 400)
            return
        result = query_db(f"DELETE FROM documents WHERE id = {doc_id};")
        self.send_json({"status": "deleted" if result.get("status") == "ok" else "error"})

    def handle_search(self, body):
        query = body.get("query", "")
        department = body.get("department", "")
        conditions = []
        if query:
            # VaultDB 1.2.0: no LIKE, use exact match or multiple OR conditions
            conditions.append(f"(title = '{query}' OR doc_number = '{query}')")
        if department:
            conditions.append(f"department = '{department}'")
        where = " AND ".join(conditions) if conditions else "1=1"
        sql = (
            f"SELECT id, doc_number, title, created_at, status, department, author, file_size "
            f"FROM documents WHERE {where} ORDER BY created_at DESC;"
        )
        result = query_db(sql)
        docs = []
        for row in result.get("rows", []):
            docs.append({
                "id": int(row[0]), "doc_number": row[1], "title": row[2],
                "created_at": row[3], "status": row[4], "department": row[5],
                "author": row[6], "file_size": int(row[7]) if row[7] else 0
            })
        self.send_json({"documents": docs})

    def handle_list_versions(self, doc_id):
        if not doc_id:
            self.send_json({"error": "doc_id required"}, 400)
            return
        result = query_db(
            f"SELECT id, version_number, content_hash, changes_description, "
            f"changed_by, changed_at FROM document_versions WHERE doc_id = {doc_id} "
            f"ORDER BY version_number;"
        )
        versions = []
        for row in result.get("rows", []):
            versions.append({
                "id": int(row[0]), "version_number": int(row[1]),
                "content_hash": row[2], "changes_description": row[3],
                "changed_by": row[4], "changed_at": row[5]
            })
        self.send_json({"versions": versions})

    def handle_stats(self):
        r1 = query_db("SELECT COUNT(*) FROM documents;")
        r2 = query_db("SELECT department, COUNT(*) FROM documents GROUP BY department;")
        r3 = query_db("SELECT COUNT(*) FROM document_versions;")
        self.send_json({
            "total_documents": int(r1.get("rows", [["0"]])[0][0]),
            "total_versions": int(r3.get("rows", [["0"]])[0][0]),
            "by_department": {row[0]: int(row[1]) for row in r2.get("rows", [])}
        })

    def handle_audit(self):
        result = query_db(
            "SELECT username, action, object_type, details, occurred_at "
            "FROM vaultdb_audit_log ORDER BY occurred_at DESC LIMIT 50;"
        )
        entries = []
        for row in result.get("rows", []):
            entries.append({
                "username": row[0], "action": row[1], "object_type": row[2],
                "details": row[3], "occurred_at": row[4]
            })
        self.send_json({"audit": entries})

    def do_OPTIONS(self):
        self.send_response(200)
        self.send_header("Access-Control-Allow-Origin", "*")
        self.send_header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
        self.send_header("Access-Control-Allow-Headers", "Content-Type")
        self.end_headers()

    def log_message(self, format, *args):
        pass  # Silence request logging


if __name__ == "__main__":
    os.makedirs(STATIC_DIR, exist_ok=True)
    server = HTTPServer(("0.0.0.0", 3000), DocVaultHandler)
    print("DocVault Web UI: http://localhost:3000")
    server.serve_forever()
