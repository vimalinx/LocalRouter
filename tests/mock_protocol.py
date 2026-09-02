#!/usr/bin/env python3
import json
import os
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

EXPECTED_AUTH = os.environ.get("PROTOCOL_TEST_AUTH", "fixture-value-only")
VIDEO_POLL_COUNT = 0


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, _format, *_args):
        return

    def send_json(self, status, payload):
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def authorized(self):
        return self.headers.get("Authorization") == f"Bearer {EXPECTED_AUTH}"

    def do_GET(self):
        global VIDEO_POLL_COUNT
        if self.path == "/health":
            return self.send_json(200, {"ok": True})
        if self.path == "/models":
            if not self.authorized():
                return self.send_json(401, {"error": "bad upstream auth"})
            return self.send_json(200, {"object": "list", "data": [
                {"id": "fixture-model-alpha", "object": "model", "owned_by": "fixture"},
            ]})
        if self.path == "/native/jobs/job-1":
            if self.headers.get("Authorization") != "Bearer fixture-account-b":
                return self.send_json(409, {"error": "job affinity lost"})
            VIDEO_POLL_COUNT += 1
            if VIDEO_POLL_COUNT == 1:
                return self.send_json(200, {"status": "pending"})
            return self.send_json(200, {"status": "done", "result": {"url": "https://example.invalid/video.mp4"}})
        return self.send_json(404, {"error": "not found"})

    def do_POST(self):
        parsed = urlparse(self.path)
        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length) if length else b"{}"
        if parsed.path == "/native/jobs":
            if not self.headers.get("Idempotency-Key"):
                return self.send_json(400, {"error": "missing idempotency key"})
            key = self.headers.get("Authorization", "")
            if key == "Bearer fixture-account-a":
                self.send_response(429)
                self.send_header("Content-Type", "application/json")
                self.send_header("Retry-After", "1")
                payload = b'{"error":"rate limited"}'
                self.send_header("Content-Length", str(len(payload)))
                self.end_headers()
                self.wfile.write(payload)
                return
            if key != "Bearer fixture-account-b":
                return self.send_json(401, {"error": "bad video auth"})
            payload = json.loads(body)
            if payload != {"input": {"prompt": "e2e video"}, "task_type": "video"}:
                return self.send_json(422, {"error": "request transform mismatch", "received": payload})
            return self.send_json(200, {"data": {"task_id": "job-1"}, "internal": "remove"})
        if not self.authorized():
            return self.send_json(401, {"error": "bad upstream auth"})
        if parsed.path == "/search":
            payload = json.loads(body)
            return self.send_json(200, {
                "ok": True,
                "query": payload.get("query"),
                "mode": parse_qs(parsed.query).get("mode", [""])[0],
                "client_authorization_forwarded": self.headers.get("X-Client-Authorization", "") != "",
            })
        if parsed.path == "/answer":
            chunks = [b"data: protocol-stream-ok\n\n", b"data: [DONE]\n\n"]
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Transfer-Encoding", "chunked")
            self.end_headers()
            for chunk in chunks:
                self.wfile.write(f"{len(chunk):x}\r\n".encode() + chunk + b"\r\n")
                self.wfile.flush()
            self.wfile.write(b"0\r\n\r\n")
            self.wfile.flush()
            return
        return self.send_json(404, {"error": "not found"})


if __name__ == "__main__":
    ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), Handler).serve_forever()
