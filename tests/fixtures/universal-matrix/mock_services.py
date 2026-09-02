#!/usr/bin/env python3
"""Loopback-only deterministic services for the universal LocalRouter matrix."""

import argparse
import base64
import json
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse


HEADER_PLACEHOLDER = "matrix-header-placeholder"
POOL_A_PLACEHOLDER = "matrix-pool-a-placeholder"
POOL_B_PLACEHOLDER = "matrix-pool-b-placeholder"
ADAPTER_PLACEHOLDER = "matrix-adapter-placeholder"

STATE_LOCK = threading.Lock()
STATE = {
    "rest_calls": 0,
    "header_auth_ok": False,
    "consumer_auth_leaked": False,
    "client_secret_leaked": False,
    "binary_calls": 0,
    "stream_calls": 0,
    "create_attempts": 0,
    "create_credentials": [],
    "idempotency_keys": [],
    "poll_attempts": 0,
    "affinity_ok": True,
    "adapter_echo_calls": 0,
    "adapter_unknown_calls": 0,
    "adapter_fixed_target_ok": True,
    "adapter_auth_ok": True,
}


def update_state(**values):
    with STATE_LOCK:
        STATE.update(values)


def append_state(key, value):
    with STATE_LOCK:
        STATE[key].append(value)


def increment(key):
    with STATE_LOCK:
        STATE[key] += 1
        return STATE[key]


class CommonHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, _format, *_args):
        return

    def send_bytes(self, status, body, content_type="application/octet-stream"):
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
        self.wfile.flush()

    def send_json(self, status, payload):
        self.send_bytes(
            status,
            json.dumps(payload, separators=(",", ":")).encode(),
            "application/json",
        )

    def read_body(self):
        length = int(self.headers.get("Content-Length", "0"))
        return self.rfile.read(length) if length else b""


class UpstreamHandler(CommonHandler):
    def record_header_boundary(self):
        update_state(
            header_auth_ok=self.headers.get("X-Matrix-Key")
            == f"Token {HEADER_PLACEHOLDER}",
            consumer_auth_leaked=self.headers.get("Authorization") is not None,
            client_secret_leaked=self.headers.get("X-Client-Secret") is not None,
        )

    def do_GET(self):
        parsed = urlparse(self.path)
        if parsed.path == "/healthz":
            return self.send_json(200, {"ok": True})
        if parsed.path == "/__matrix/state":
            with STATE_LOCK:
                public = {
                    "rest_calls": STATE["rest_calls"],
                    "header_auth_ok": STATE["header_auth_ok"],
                    "consumer_auth_leaked": STATE["consumer_auth_leaked"],
                    "client_secret_leaked": STATE["client_secret_leaked"],
                    "binary_calls": STATE["binary_calls"],
                    "stream_calls": STATE["stream_calls"],
                    "create_attempts": STATE["create_attempts"],
                    "credential_order_ok": STATE["create_credentials"]
                    == [POOL_A_PLACEHOLDER, POOL_B_PLACEHOLDER],
                    "idempotency_stable": len(STATE["idempotency_keys"]) == 2
                    and len(set(STATE["idempotency_keys"])) == 1
                    and bool(STATE["idempotency_keys"][0]),
                    "poll_attempts": STATE["poll_attempts"],
                    "affinity_ok": STATE["affinity_ok"],
                    "adapter_echo_calls": STATE["adapter_echo_calls"],
                    "adapter_unknown_calls": STATE["adapter_unknown_calls"],
                    "adapter_fixed_target_ok": STATE["adapter_fixed_target_ok"],
                    "adapter_auth_ok": STATE["adapter_auth_ok"],
                }
            return self.send_json(200, public)
        if parsed.path == "/rest/status":
            increment("rest_calls")
            return self.send_json(
                200,
                {
                    "service": "plain-rest",
                    "status": "ok",
                    "upstream_auth_absent": self.headers.get("Authorization") is None,
                },
            )
        if parsed.path == "/catalog/models":
            self.record_header_boundary()
            if not STATE["header_auth_ok"]:
                return self.send_json(401, {"error": "header auth rejected"})
            return self.send_json(
                200,
                {
                    "object": "list",
                    "data": [
                        {
                            "id": "matrix-text-v1",
                            "object": "model",
                            "owned_by": "matrix-fixture",
                        }
                    ],
                },
            )
        if parsed.path == "/async/jobs/matrix-job-1":
            increment("poll_attempts")
            if self.headers.get("Authorization") != f"Bearer {POOL_B_PLACEHOLDER}":
                update_state(affinity_ok=False)
                return self.send_json(409, {"error": "affinity lost"})
            if STATE["poll_attempts"] == 1:
                return self.send_json(200, {"status": "running"})
            return self.send_json(
                200,
                {
                    "status": "done",
                    "result": {"uri": "matrix://job/matrix-job-1/result"},
                },
            )
        return self.send_json(404, {"error": "not found"})

    def do_POST(self):
        parsed = urlparse(self.path)
        body = self.read_body()
        if parsed.path == "/chat/completions":
            self.record_header_boundary()
            if not STATE["header_auth_ok"]:
                return self.send_json(401, {"error": "header auth rejected"})
            payload = json.loads(body or b"{}")
            return self.send_json(
                200,
                {
                    "model": payload.get("model"),
                    "choices": [
                        {"message": {"role": "assistant", "content": "matrix-chat-ok"}}
                    ],
                },
            )
        if parsed.path == "/events":
            increment("stream_calls")
            first = b"data: matrix-stream-first\n\n"
            terminal = b"data: [DONE]\n\n"
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Transfer-Encoding", "chunked")
            self.end_headers()
            self.wfile.write(f"{len(first):x}\r\n".encode() + first + b"\r\n")
            self.wfile.flush()
            time.sleep(1.2)
            self.wfile.write(
                f"{len(terminal):x}\r\n".encode() + terminal + b"\r\n0\r\n\r\n"
            )
            self.wfile.flush()
            return
        if parsed.path == "/binary/echo":
            increment("binary_calls")
            return self.send_bytes(200, body)
        if parsed.path == "/async/jobs":
            increment("create_attempts")
            credential = self.headers.get("Authorization", "").removeprefix("Bearer ")
            append_state("create_credentials", credential)
            append_state("idempotency_keys", self.headers.get("Idempotency-Key", ""))
            if credential == POOL_A_PLACEHOLDER:
                self.send_response(429)
                self.send_header("Content-Type", "application/json")
                self.send_header("Retry-After", "1")
                body = b'{"error":"fixture rate limit"}'
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
                return
            if credential != POOL_B_PLACEHOLDER:
                return self.send_json(401, {"error": "pool auth rejected"})
            return self.send_json(200, {"job_id": "matrix-job-1"})
        return self.send_json(404, {"error": "not found"})


class AdapterHandler(CommonHandler):
    upstream_origin = ""

    def do_GET(self):
        if self.path == "/healthz":
            return self.send_json(200, {"ok": True})
        return self.send_json(404, {"error": "not found"})

    def do_POST(self):
        if self.path != "/invoke":
            return self.send_json(404, {"error": "not found"})
        try:
            envelope = json.loads(self.read_body())
            operation = envelope["operation_id"]
            request = envelope["request"]
        except (KeyError, TypeError, json.JSONDecodeError):
            return self.send_json(400, {"error": "invalid envelope"})
        expected_url = f"{self.upstream_origin}/adapter-native/invoke"
        fixed_target_ok = request.get("url") == expected_url
        auth_values = request.get("headers", {}).get("X-Adapter-Key", [])
        auth_ok = auth_values == [ADAPTER_PLACEHOLDER]
        update_state(
            adapter_fixed_target_ok=STATE["adapter_fixed_target_ok"] and fixed_target_ok,
            adapter_auth_ok=STATE["adapter_auth_ok"] and auth_ok,
        )
        if operation == "adapter.echo":
            increment("adapter_echo_calls")
            request_body = base64.b64decode(request.get("body_base64", ""), validate=True)
            supplied = json.loads(request_body or b"{}")
            response = {
                "adapter": "ok",
                "fixed_target": fixed_target_ok,
                "auth": auth_ok,
                "untrusted_target_was_data": "target_url" in supplied,
            }
            return self.send_json(
                200,
                {
                    "status": 200,
                    "headers": {"Content-Type": ["application/json"]},
                    "body_base64": base64.b64encode(
                        json.dumps(response, separators=(",", ":")).encode()
                    ).decode(),
                    "outcome": "complete",
                },
            )
        if operation == "adapter.unknown":
            increment("adapter_unknown_calls")
            return self.send_json(
                200,
                {
                    "status": 520,
                    "headers": {"Content-Type": ["application/json"]},
                    "body_base64": base64.b64encode(b'{"error":"ambiguous fixture"}').decode(),
                    "outcome": "unknown",
                },
            )
        return self.send_json(400, {"error": "unsupported operation"})


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("upstream_port", type=int)
    parser.add_argument("adapter_port", type=int)
    args = parser.parse_args()
    upstream = ThreadingHTTPServer(("127.0.0.1", args.upstream_port), UpstreamHandler)
    AdapterHandler.upstream_origin = f"http://127.0.0.1:{args.upstream_port}"
    adapter = ThreadingHTTPServer(("127.0.0.1", args.adapter_port), AdapterHandler)
    thread = threading.Thread(target=adapter.serve_forever, daemon=True)
    thread.start()
    try:
        upstream.serve_forever()
    finally:
        adapter.shutdown()
        adapter.server_close()
        upstream.server_close()


if __name__ == "__main__":
    main()
