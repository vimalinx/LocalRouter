#!/usr/bin/env python3
"""Black-box acceptance of the installed binary using disposable fixture state.

No administrator credential is read; no management API mutation is performed.
Only the fixture database/policies are seeded, never production configuration.
"""
import argparse
import concurrent.futures
import hashlib
import json
import math
import os
from pathlib import Path
import secrets
import socket
import sqlite3
import subprocess
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.error import HTTPError
from urllib.request import build_opener, ProxyHandler, Request

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--binary', type=Path, required=True)
parser.add_argument('--output-dir', type=Path)
parser.add_argument('--hold', action='store_true', help='keep the fixture available for browser acceptance until continue-ui exists')
ARGS = parser.parse_args()
ROOT = (ARGS.output_dir or Path(tempfile.mkdtemp(prefix='localrouter-agent-billing-'))).resolve()
ROOT.mkdir(mode=0o700, parents=True, exist_ok=True)
STATE = Path(tempfile.mkdtemp(prefix="fixture-", dir=ROOT))
BINARY = ARGS.binary.resolve()
os.umask(0o077)
OPENER = build_opener(ProxyHandler({}))
ENTERED, RELEASE = threading.Event(), threading.Event()
CALLS = []
CALL_LOCK = threading.Lock()
REPORT = {"binary": str(BINARY), "version": subprocess.check_output([str(BINARY), "--version"], text=True).strip(),
          "sha256": hashlib.sha256(BINARY.read_bytes()).hexdigest(), "fixture": str(STATE),
          "upstream": "loopback mock only; no paid provider", "checks": [], "observations": []}


class Upstream(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_POST(self):
        doc = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        with CALL_LOCK:
            CALLS.append({"model": doc["model"], "stream": bool(doc.get("stream")),
                          "upstream_auth_correct": self.headers.get("Authorization") == "Bearer fixture-upstream-key"})
        if doc["messages"][0]["content"] == "hold":
            ENTERED.set()
            if not RELEASE.wait(15):
                self.send_error(504)
                return
        usage = {"prompt_tokens": 100, "completion_tokens": 20, "total_tokens": 120}
        if doc["model"] != "unpriced":
            usage["cost_usd"] = 0.04 if doc["model"] == "premium" else (0.02 if doc.get("stream") else 0.01)
        if doc.get("stream"):
            chunks = [{"id": "fixture", "model": doc["model"], "choices": [{"index": 0, "delta": {"content": "fixture-ok"}, "finish_reason": None}]},
                      {"id": "fixture", "model": doc["model"], "choices": [], "usage": usage}]
            body = ("".join("data: " + json.dumps(c) + "\n\n" for c in chunks) + "data: [DONE]\n\n").encode()
            kind = "text/event-stream"
        else:
            body = json.dumps({"id": "fixture", "model": doc["model"], "choices": [{"index": 0, "message": {"role": "assistant", "content": "fixture-ok"}, "finish_reason": "stop"}], "usage": usage}).encode()
            kind = "application/json"
        self.send_response(200)
        self.send_header("Content-Type", kind)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


UPSTREAM = ThreadingHTTPServer(("127.0.0.1", 0), Upstream)
threading.Thread(target=UPSTREAM.serve_forever, daemon=True).start()
with socket.socket() as candidate:
    candidate.bind(("127.0.0.1", 0))
    PORT = candidate.getsockname()[1]
BASE = f"http://127.0.0.1:{PORT}"
REPORT["fixture_url"] = BASE
ENV = {key: value for key, value in os.environ.items() if not key.startswith("LOCAL_GATEWAY_") and key not in ("PORT", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy")}
for kind in ("CONFIG", "DATA", "STATE", "CACHE", "PROTOCOL"):
    ENV[f"LOCAL_GATEWAY_{kind}_DIR"] = str(STATE / kind.lower())
ENV.update(LOCAL_GATEWAY_HOST="127.0.0.1", LOCAL_GATEWAY_PORT=str(PORT), LOCAL_GATEWAY_LAN_ENABLED="false", LOCAL_GATEWAY_UPDATE_CHECK_ENABLED="false", GIN_MODE="release")
PROCESS = None
LOG = (STATE / "gateway.log").open("ab")
TOKENS, IDS = {}, {}
DATABASE = STATE / "data/localrouter.db"


def request(path, body=None, token=None):
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = "Bearer sk-" + TOKENS[token]
    req = Request(BASE + path, data=None if body is None else json.dumps(body).encode(), headers=headers)
    try:
        with OPENER.open(req, timeout=20) as response:
            return response.status, response.read().decode()
    except HTTPError as error:
        return error.code, error.read().decode()


def start():
    global PROCESS
    PROCESS = subprocess.Popen([str(BINARY), "--log-dir", str(STATE / "logs")], cwd=STATE, env=ENV, stdout=LOG, stderr=LOG)
    for _ in range(100):
        if PROCESS.poll() is not None:
            raise RuntimeError(f"fixture gateway exited: {PROCESS.returncode}; see private gateway.log")
        try:
            if request("/healthz")[0] == 200:
                return
        except OSError:
            pass
        time.sleep(0.05)
    raise RuntimeError("fixture gateway did not become healthy")


def stop():
    global PROCESS
    if PROCESS and PROCESS.poll() is None:
        PROCESS.terminate()
        try:
            PROCESS.wait(8)
        except subprocess.TimeoutExpired:
            PROCESS.kill()
            PROCESS.wait()
    PROCESS = None


def check(name, condition, details):
    item = {"name": name, "pass": bool(condition), "details": details}
    REPORT["checks"].append(item)
    print(json.dumps(item, ensure_ascii=False), flush=True)
    if not condition:
        raise AssertionError(name)


def call(agent, model="cheap", stream=False, content="fixture"):
    return request("/v1/chat/completions", {"model": model, "stream": stream, "stream_options": {"include_usage": True}, "messages": [{"role": "user", "content": content}]}, agent)


def usage():
    status, raw = request("/local/api/agent-usage")
    assert status == 200, raw
    return {item["agent_code"]: item for item in json.loads(raw)["data"] if item["agent_code"]}


def wait_usage(agent, count):
    for _ in range(100):
        values = usage()
        if values.get(agent, {}).get("requests") == count:
            return values
        time.sleep(0.02)
    raise AssertionError(f"usage count did not settle for {agent}: {values}")


def policies(a, b, reset=False):
    assert PROCESS is None, "policy fixtures may only change while stopped"
    doc = {"schema_version": "1", "policies": {str(IDS[k]): {"token_id": IDS[k], **v} for k, v in (("agent-a", a), ("agent-b", b))}}
    (STATE / "data/token-policies.json").write_text(json.dumps(doc))
    if reset:
        (STATE / "data/token-policy-usage.json").unlink(missing_ok=True)


def upstream_count():
    with CALL_LOCK:
        return len(CALLS)


def run():
    start()
    stop()
    with sqlite3.connect(DATABASE) as db:
        user = db.execute("SELECT id FROM users WHERE role=100").fetchone()[0]
        for agent in ("agent-a", "agent-b"):
            TOKENS[agent] = secrets.token_hex(32)
            IDS[agent] = db.execute('INSERT INTO tokens (user_id,key,status,name,agent_code,agent_name,workspace,runtime,created_time,expired_time,unlimited_quota) VALUES (?,?,1,?,?,?,?,?,?, -1,1)',
                                  (user, TOKENS[agent], agent, agent, agent.upper(), "/fixture/" + agent, "acceptance", int(time.time()))).lastrowid
        db.execute('INSERT INTO channels (type,key,status,name,weight,created_time,base_url,models,"group",priority,auto_ban) VALUES (1,?,1,?,100,?,?,?,\'default\',0,0)',
                   ("fixture-upstream-key", "Fixture shared channel", int(time.time()), f"http://127.0.0.1:{UPSTREAM.server_port}", "cheap,premium,unpriced"))
    policies({"daily_request_limit": 2}, {"daily_request_limit": 20})
    start()
    a1, ar1 = call("agent-a")
    a2, ar2 = call("agent-a", stream=True)
    b1, br1 = call("agent-b", "premium")
    values = wait_usage("agent-b", 1)
    a, b = values["agent-a"], values["agent-b"]
    check("independent_keys_and_usage", TOKENS["agent-a"] != TOKENS["agent-b"] and (a1, a2, b1) == (200, 200, 200) and a["requests"] == 2 and b["requests"] == 1 and (a["prompt_tokens"], a["completion_tokens"], b["prompt_tokens"], b["completion_tokens"]) == (200, 40, 100, 20), {"A_requests": a["requests"], "B_requests": b["requests"], "A_tokens": a["prompt_tokens"] + a["completion_tokens"], "B_tokens": b["prompt_tokens"] + b["completion_tokens"]})
    check("reported_cost_json_and_sse", math.isclose(a["cost_usd"], 0.03) and math.isclose(b["cost_usd"], 0.04) and a["cost_status"] == b["cost_status"] == "reported" and "[DONE]" in ar2, {"A_usd": a["cost_usd"], "B_usd": b["cost_usd"], "status": a["cost_status"]})
    REPORT["priced_usage_snapshot"] = values
    count = upstream_count()
    denied, raw = call("agent-a")
    check("daily_quota_blocks_before_upstream", denied == 429 and "daily request limit" in raw and upstream_count() == count, {"status": denied, "upstream_calls_added": upstream_count() - count})
    check("B_survives_A_quota_exhaustion", call("agent-b", "premium")[0] == 200, {"B_status": 200})
    stop()
    start()
    count = upstream_count()
    denied, raw = call("agent-a")
    check("daily_quota_survives_process_restart", denied == 429 and upstream_count() == count, {"status": denied})
    status, logs_raw = request("/local/api/logs?page=1&page_size=100")
    logs = json.loads(logs_raw)["data"]["items"]
    a_logs = [row for row in logs if row["token_id"] == IDS["agent-a"]]
    b_logs = [row for row in logs if row["token_id"] == IDS["agent-b"]]
    check("logs_bind_agent_model_and_channel", status == 200 and len(a_logs) == len(b_logs) == 2 and all(row["token_name"] == "agent-a" and row["model_name"] == "cheap" and row["channel_name"] == "Fixture shared channel" for row in a_logs) and all(row["model_name"] == "premium" for row in b_logs), {"A_rows": len(a_logs), "B_rows": len(b_logs), "fields": ["token_id", "token_name", "model_name", "channel_name"]})
    check("upstream_receives_only_its_own_key", all(row["upstream_auth_correct"] for row in CALLS), {"requests_verified": upstream_count()})

    stop()
    policies({"max_in_flight": 1}, {"max_in_flight": 1}, reset=True)
    start()
    with concurrent.futures.ThreadPoolExecutor(max_workers=1) as pool:
        pending = pool.submit(call, "agent-a", "cheap", False, "hold")
        assert ENTERED.wait(5), "held upstream request was not reached"
        count = upstream_count()
        denied, raw = call("agent-a")
        blocked_before_upstream = upstream_count() == count
        b_status, _ = call("agent-b", "premium")
        RELEASE.set()
        a_status, _ = pending.result(timeout=10)
    check("concurrency_is_per_agent", denied == 429 and "concurrency limit" in raw and blocked_before_upstream and b_status == a_status == 200, {"second_A": denied, "parallel_B": b_status, "first_A": a_status})
    check("concurrency_slot_is_released", call("agent-a")[0] == 200, {"A_after_completion": 200})

    stop()
    policies({"requests_per_minute": 1}, {"requests_per_minute": 10}, reset=True)
    start()
    if time.time() % 60 > 57:
        time.sleep(60 - time.time() % 60 + 0.05)
    minute = int(time.time() // 60)
    first, _ = call("agent-a")
    count = upstream_count()
    denied, raw = call("agent-a")
    blocked_before_upstream = upstream_count() == count
    other, _ = call("agent-b", "premium")
    check("RPM_is_per_agent", first == other == 200 and denied == 429 and "minute request limit" in raw and blocked_before_upstream and int(time.time() // 60) == minute, {"first_A": first, "second_A": denied, "B": other})

    stop()
    policies({"models": ["cheap"]}, {"models": ["premium", "unpriced"]}, reset=True)
    start()
    count = upstream_count()
    denied, raw = call("agent-a", "premium")
    blocked_before_upstream = upstream_count() == count
    other, _ = call("agent-b", "premium")
    check("model_scope_is_per_agent", denied == 403 and blocked_before_upstream and other == 200, {"A_premium": denied, "B_premium": other})
    b_before = usage()["agent-b"]
    unknown, _ = call("agent-b", "unpriced")
    b_after = wait_usage("agent-b", b_before["requests"] + 1)["agent-b"]
    check("missing_price_is_not_claimed_complete", unknown == 200 and b_after["cost_status"] == "partial" and math.isclose(b_after["cost_usd"], b_before["cost_usd"]), {"status": b_after["cost_status"], "known_cost_usd": b_after["cost_usd"], "added_unpriced_requests": 1})
    REPORT["before_revoke"] = usage()
    # Real requests share a durable monetary budget, independently of rate limits.
    base_a = round(usage()['agent-a']['cost_usd'] * 1_000_000)
    stop()
    policies({'monthly_budget_usd_micros': base_a + 30_000, 'request_reserve_usd_micros': 20_000}, {}, reset=True)
    start()
    check('money_budget_accepts_JSON_and_SSE', call('agent-a')[0] == 200 and call('agent-a', stream=True)[0] == 200, {'new_cost_usd': 0.03})
    count = upstream_count()
    denied, raw = call('agent-a')
    check('money_budget_blocks_before_upstream', denied == 429 and 'agent_monthly_budget_exceeded' in raw and upstream_count() == count, {'status': denied})
    stop()
    start()
    denied, raw = call('agent-a')
    check('money_budget_survives_restart', denied == 429 and 'agent_monthly_budget_exceeded' in raw, {'status': denied})
    check('money_budget_is_per_agent', call('agent-b', 'premium')[0] == 200, {'B_status': 200})
    base_b = round(usage()['agent-b']['cost_usd'] * 1_000_000)
    stop()
    policies({}, {'monthly_budget_usd_micros': base_b + 40_000, 'request_reserve_usd_micros': 20_000}, reset=True)
    start()
    check('unknown_price_reserves_money', call('agent-b','unpriced')[0] == 200 and call('agent-b','unpriced')[0] == 200, {'held_usd': 0.04})
    count = upstream_count()
    denied, raw = call('agent-b','unpriced')
    balance = usage()['agent-b']['budget']
    check('unknown_price_cannot_spend_as_free', denied == 429 and upstream_count() == count and balance['held_usd_micros'] == 40_000, {'status': denied, 'held_usd_micros': balance['held_usd_micros']})
    stop()
    policies({'monthly_budget_usd_micros': base_a + 500_000, 'request_reserve_usd_micros': 1_000}, {}, reset=True)
    start()
    check('larger_provider_charge_is_observed', call('agent-a','premium')[0] == 200, {'provider_charge_usd': 0.04})
    count = upstream_count()
    denied, raw = call('agent-a')
    check('over_reservation_charge_pauses_A', denied == 403 and 'agent_budget_reconciliation_required' in raw and upstream_count() == count, {'status': denied})
    stop()
    policies({'monthly_budget_usd_micros': base_a + 500_000, 'request_reserve_usd_micros': 1_000}, {'monthly_budget_usd_micros': base_b + 120_000, 'request_reserve_usd_micros': 40_000})
    start()

    with sqlite3.connect(DATABASE) as db:
        db.execute("UPDATE tokens SET status=2, deleted_at=CURRENT_TIMESTAMP WHERE id=?", (IDS["agent-a"],))
    count = upstream_count()
    revoked, raw = call("agent-a")
    blocked_before_upstream = upstream_count() == count
    surviving, _ = call("agent-b", "premium")
    check("revocation_is_immediate_and_independent", revoked == 401 and surviving == 200 and blocked_before_upstream, {"revoked_A": revoked, "B": surviving})
    after = usage()
    _, raw = request("/local/api/logs?page=1&page_size=100")
    retained = [row for row in json.loads(raw)["data"]["items"] if row["token_id"] == IDS["agent-a"]]
    REPORT["observations"].append({"name": "revoked_agent_history_visibility", "agent_still_in_usage_summary": "agent-a" in after, "historical_log_rows_retained": len(retained), "impact": "Revoked Agent remains in summary and keeps billing and logs."})
    check('revoked_agent_keeps_summary', 'agent-a' in after and after['agent-a']['revoked'] and after['agent-a']['requests'] > 0 and after['agent-a']['cost_usd'] > 0, {'requests': after.get('agent-a',{}).get('requests'), 'cost_usd': after.get('agent-a',{}).get('cost_usd')})
    status, raw = request(f"/local/api/logs?token_id={IDS['agent-a']}&q=agent-a&page=1&page_size=2")
    matches = json.loads(raw)['data']
    check('server_search_includes_revoked_agent', status == 200 and matches['total'] == len(retained) and len(matches['items']) == 2 and all(r['revoked'] for r in matches['items']), {'total': matches['total'], 'first_page_rows': len(matches['items'])})
    status, raw = request(f"/local/api/agent-billing/{IDS['agent-a']}?page=1&page_size=100")
    billed = json.loads(raw)['data']
    check('revoked_agent_keeps_billing_details', status == 200 and billed['total'] == len(retained) and any(r['service'] == 'Fixture shared channel' and r['model'] == 'premium' for r in billed['items']), {'billing_rows': billed['total']})
    REPORT['after_revoke'] = after
    print(json.dumps(REPORT["observations"][-1]), flush=True)
    REPORT["result"] = "passed"
    if ARGS.hold:
        (ROOT / 'ui.ready').write_text(BASE)
        print('UI_READY ' + BASE, flush=True)
        for _ in range(9000):
            if (ROOT / 'continue-ui').exists(): break
            time.sleep(0.1)


try:
    run()
except BaseException as error:
    REPORT["result"] = "failed"
    REPORT["error"] = str(error)
    raise
finally:
    RELEASE.set()
    stop()
    UPSTREAM.shutdown()
    UPSTREAM.server_close()
    LOG.close()
    (ROOT / "report.json").write_text(json.dumps(REPORT, indent=2, ensure_ascii=False) + "\n")
    print("REPORT " + str(ROOT / "report.json"), flush=True)
