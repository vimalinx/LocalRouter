#!/usr/bin/env python3
"""Exercise Agent CLI and human decisions against an isolated real gateway."""
import argparse
import copy
import http.server
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request

parser = argparse.ArgumentParser()
parser.add_argument('--demo', action='store_true')
args = parser.parse_args()
project = Path(__file__).resolve().parents[1]
root = Path(tempfile.mkdtemp(prefix='localrouter-service-workspace-'))
os.chmod(root, 0o700)
class Provider(http.server.BaseHTTPRequestHandler):
    calls = 0
    requests = []
    model_calls = 0
    generation_calls = 0
    invalid_catalog = False
    fail_generation = False
    def do_GET(self):
        type(self).calls += 1
        type(self).requests.append(self.path)
        if self.path == '/models':
            type(self).model_calls += 1
            value = {'unexpected': True} if type(self).invalid_catalog else {'data': [{'id': 'fixture-model'}]}
            body = json.dumps(value).encode()
        else:
            body = json.dumps({'id': 'fixture-document', 'state': 'succeeded', 'usage': {'pages': 3}, 'results': [{'title': '隔离演示文档', 'snippet': '这次响应来自本机测试服务。'}]}, ensure_ascii=False).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def do_POST(self):
        type(self).generation_calls += 1
        self.rfile.read(int(self.headers.get('Content-Length', '0')))
        body = b'{"error":"fixture failure"}' if type(self).fail_generation else b'data: {"choices":[{"delta":{"content":"ok"}}]}\n\ndata: [DONE]\n\n'
        self.send_response(502 if type(self).fail_generation else 200)
        self.send_header('Content-Type', 'application/json' if type(self).fail_generation else 'text/event-stream')
        self.send_header('Content-Length', str(len(body))); self.end_headers(); self.wfile.write(body)
    def log_message(self, *unused): pass
provider = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Provider)
threading.Thread(target=provider.serve_forever, daemon=True).start()
with socket.socket() as probe:
    probe.bind(('127.0.0.1', 0))
    port = probe.getsockname()[1]
base = f'http://127.0.0.1:{port}'
env = {key: value for key, value in os.environ.items() if not key.startswith(('LOCALROUTER_', 'LOCAL_GATEWAY_'))}
for name in ('config', 'data', 'state', 'cache'):
    env[f'LOCAL_GATEWAY_{name.upper()}_DIR'] = str(root / name)
env.update(LOCAL_GATEWAY_PORT=str(port), LOCAL_GATEWAY_HOST='127.0.0.1', LOCAL_GATEWAY_UPDATE_CHECK_ENABLED='false', GIN_MODE='release')
log = (root / 'server.log').open('w')
process = subprocess.Popen([str(project / 'gateway/localrouter'), '--log-dir', str(root / 'logs')], env=env, stdout=log, stderr=subprocess.STDOUT)
def api(path, method='GET', body=None):
    request = urllib.request.Request(base + path, method=method, data=None if body is None else json.dumps(body).encode(), headers={'Content-Type': 'application/json'})
    with urllib.request.urlopen(request, timeout=5) as response:
        return json.load(response)
def cli(*arguments):
    result = subprocess.run([str(project / 'tools/lr'), *arguments], env=env, cwd=root, capture_output=True, text=True, timeout=20)
    if result.returncode:
        try: message = json.dumps({k:v for k,v in json.loads(result.stdout).items() if k in ('message','code','reason','ready','next_action')})
        except (ValueError, AttributeError): message = ''
        raise AssertionError(f'lr {arguments[0]} failed: {message or result.stderr[:300]}')
    return result.stdout if arguments[0] == "tree" else json.loads(result.stdout)
def payload(result):
    return result.get('structuredContent', result.get('result', {}).get('structuredContent', result))
try:
    for _ in range(120):
        try:
            if api('/healthz')['ok']: break
        except (OSError, ValueError):
            if process.poll() is not None: raise RuntimeError('isolated gateway exited; inspect its fixture log')
            time.sleep(.1)
    else: raise RuntimeError('isolated gateway did not start')
    token_id = api('/local/api/tokens', 'POST', {'name':'service-fixture','agent_code':'service-fixture','agent_name':'研究 Agent · 隔离演示','workspace':str(root),'runtime':'fixture','status':1,'expired_time':-1})['data']['id']
    key = api(f'/local/api/tokens/{token_id}/reveal', 'POST')['data']['key']
    token_path = root / 'service-token'
    token_path.write_text(key + '\n'); token_path.chmod(0o600); del key
    env.update(LOCALROUTER_BASE_URL=base, LOCALROUTER_API_TOKEN_FILE=str(token_path), LOCALROUTER_TASK_ID='document-research-demo')
    cli('status'); cli('tree')
    templates = payload(cli('setup', 'templates'))['templates']
    metadata = next(item for item in templates if item['id'] == 'read-api')
    assert 'example' not in metadata
    template = payload(cli('setup','template',metadata['id'],metadata['version']))['template']
    assert cli('init')['ready'] is True
    definition = copy.deepcopy(template['example'])
    definition.update(id='demo-search', name='文档检索服务', description='本机隔离服务，用于验证 Agent 接入、授权与调用追踪。', base_url=f'http://127.0.0.1:{provider.server_port}')
    definition['routes'].append({'operation_id':'status','methods':['GET'],'path':'/jobs/{jobId}','summary':'Fixture path lookup'})
    definition['routes'].extend([
        {'operation_id':'models','capabilities':['ai.models'],'methods':['GET'],'path':'/models','summary':'Model catalogue'},
        {'operation_id':'generate','capabilities':['ai.chat'],'methods':['POST'],'path':'/chat/completions','summary':'Streaming fixture','streaming':True,'request_example':{'model':'fixture-model','messages':[{'role':'user','content':'hi'}],'max_tokens':10},'retry':{'mode':'never','transport_errors':False}},
    ])
    definition['routes'][0]['metering'] = {'resource_id_path':'id','state_path':'state','units':[{'unit':'page','path':'usage.pages','source':'response','mode':'snapshot'}]}
    proposal_input = {'kind':'connection','reason':'为资料研究任务接入文档检索，只申请 search 操作。准备与授权不会调用上游。','connection':{'template_id':'read-api','template_version':'1','definition':definition},'bundle':{'id':'research-kit','name':'资料研究工具包','description':'受限的文档检索权限','members':[{'pack':'demo-search','operations':['search','status','models','generate']}]}}
    input_path = root / 'proposal.json'; input_path.write_text(json.dumps(proposal_input))
    proposal = payload(cli('setup','prepare','@'+str(input_path)))['proposal']
    assert proposal['state'] == 'awaiting_approval' and Provider.calls == 0
    try:
        api(f"/local/api/service-proposals/{proposal['id']}/decision",'POST',{'decision':'approve','digest':'0'*64})
        raise AssertionError('stale approval accepted')
    except urllib.error.HTTPError as error:
        assert error.code == 409
    api(f"/local/api/service-proposals/{proposal['id']}/decision",'POST',{'decision':'approve','digest':proposal['digest']})
    assert Provider.calls == 0
    assert payload(cli('setup','get',proposal['id']))['proposal']['state'] == 'applied'
    cli('preflight','demo-search','search','{}','{}','{"q":"fixture"}')
    # Capture the single authorized fixture call before offline inspection.
    response = subprocess.run([str(project/'tools/lr'),'call','demo-search','search','{}','{}','{"q":"fixture"}'], env=env, capture_output=True, timeout=20)
    (root/'call-response.json').write_bytes(response.stdout)
    (root/'call-exit-status').write_text(str(response.returncode))
    assert response.returncode == 0 and Provider.calls == 1
    assert Provider.requests == ['/search?q=fixture']
    assert payload(cli('setup','verify',proposal['id']))['upstream_operation'] == 'response-received'
    traces = payload(cli('setup','traces'))
    assert traces['summary']['requests'] == 1 and traces['summary']['units'][0]['quantity'] == 3
    assert traces['summary']['unknown_costs'] == 1
    assert all(item['token_id'] == token_id for item in traces['items'])
    missing_path = subprocess.run([str(project/'tools/lr'),'call','demo-search','status'],env=env,capture_output=True,timeout=20)
    assert missing_path.returncode != 0 and Provider.calls == 1
    unsafe_path = subprocess.run([str(project/'tools/lr'),'call','demo-search','status','{}','{"jobId":".."}'],env=env,capture_output=True,timeout=20)
    assert unsafe_path.returncode != 0 and Provider.calls == 1
    cli('call','demo-search','status','{}','{"jobId":"job one"}','{"detail":"a&b / c"}')
    from urllib.parse import urlsplit, unquote, parse_qs
    observed = urlsplit(Provider.requests[-1])
    assert Provider.calls == 2 and unquote(observed.path) == '/jobs/job one', Provider.requests
    assert parse_qs(observed.query) == {'detail':['a&b / c']}, Provider.requests
    absent_template = subprocess.run([str(project/'tools/lr'),'setup','template','missing','1'],env=env,capture_output=True,timeout=20)
    assert absent_template.returncode != 0
    if not args.demo:
        # The existing bootstrap credential works by default, including streaming.
        env['LOCALROUTER_API_TOKEN_FILE'] = str(root/'data/api-token')
        ordinary = cli('init')
        assert ordinary['ready'] and not ordinary['identity_ready'] and not ordinary['identity_required'] and not ordinary['strict_mode']
        before_generations = Provider.generation_calls
        response = subprocess.run([str(project/'tools/lr'),'exec','demo-search','generate','{"model":"fixture-model","messages":[],"max_tokens":10,"stream":true}'],env=env,cwd=root,capture_output=True,text=True,timeout=20)
        assert response.returncode == 0 and response.stdout.endswith('data: [DONE]\n\n'), (response.stdout, response.stderr)
        assert Provider.generation_calls == before_generations+1
        settings = api('/local/api/service-allowance-settings')['data']
        settings = api('/local/api/service-allowance-settings','PUT',{'enabled':True,'revision':settings['revision']})['data']
        strict = subprocess.run([str(project/'tools/lr'),'init'],env=env,cwd=root,capture_output=True,text=True,timeout=20)
        assert strict.returncode != 0 and json.loads(strict.stdout)['identity_required']
        blocked = subprocess.run([str(project/'tools/lr'),'exec','demo-search','generate','{"model":"fixture-model","messages":[]}'],env=env,cwd=root,capture_output=True,text=True,timeout=20)
        assert blocked.returncode != 0 and Provider.generation_calls == before_generations+1
        api('/local/api/service-allowance-settings','PUT',{'enabled':False,'revision':settings['revision']})
        assert cli('init')['ready']
        env['LOCALROUTER_API_TOKEN_FILE'] = str(token_path)
        env.update(XDG_DATA_HOME=str(root/'client-data'), LOCALROUTER_AGENT_SESSION='fixture-session')
        bound = cli('identity','bind','service-fixture',str(token_path))
        assert bound['ready'] and bound['token_value_copied'] is False
        del env['LOCALROUTER_API_TOKEN_FILE']
        assert cli('init')['agent_code'] == 'service-fixture'
        # Another Agent in the same workspace must not reuse this binding.
        other_env = dict(env, LOCALROUTER_AGENT_SESSION='different-session')
        other = subprocess.run([str(project/'tools/lr'),'init'],env=other_env,cwd=root,capture_output=True,text=True,timeout=20)
        assert other.returncode != 0 and not json.loads(other.stdout)['ready']
        wrong = subprocess.run([str(project/'tools/lr'),'identity','bind','wrong-agent',str(token_path)],env=env,cwd=root,capture_output=True,text=True,timeout=20)
        assert wrong.returncode != 0 and cli('init')['agent_code'] == 'service-fixture'
        before_models, before_generations = Provider.model_calls, Provider.generation_calls
        response = subprocess.run([str(project/'tools/lr'),'exec','demo-search','generate','{"model":"fixture-model","messages":[{"role":"user","content":"fixture"}],"max_tokens":10,"stream":true}'],env=env,cwd=root,capture_output=True,text=True,timeout=20)
        assert response.returncode == 0, (response.stdout,response.stderr)
        assert response.stdout.endswith('data: [DONE]\n\n') and 'delta' in response.stdout
        assert Provider.model_calls == before_models+1 and Provider.generation_calls == before_generations+1
        receipt = cli('result')['results'][0]
        assert receipt['outcome'] == 'response_received' and receipt['exit_code'] == 0
        assert Path(receipt['response_file']).read_text() == response.stdout
        assert Path(receipt['response_file']).stat().st_mode & 0o777 == 0o600
        before_trace_read = Provider.generation_calls
        evidence = cli('result',receipt['id'],'--refresh')
        assert evidence['provider_replayed'] is False and evidence['gateway_evidence']['total'] >= 1
        assert Provider.generation_calls == before_trace_read
        # Gateway denial remains structured and is not presented as an empty catalogue.
        api(f'/local/api/token-policies/{token_id}','PUT',{'surfaces':['p'],'packs':['demo-search'],'operations':['generate'],'models':['*']})
        result = subprocess.run([str(project/'tools/lr'),'find','model','--exact','demo-search:fixture-model'],env=env,cwd=root,capture_output=True,text=True,timeout=20)
        data = json.loads(result.stdout)
        assert result.returncode != 0 and data['success'] is False and data['complete'] is False
        failure = data['failures'][0]
        assert failure['code'] == 'token_policy_denied' and failure['http_status'] == 403 and failure['retryable'] is False and failure['next_action']
        assert Provider.model_calls == before_models+1
        api(f'/local/api/token-policies/{token_id}','PUT',{'surfaces':['p'],'packs':['demo-search'],'operations':['*'],'models':['another-model']})
        result = subprocess.run([str(project/'tools/lr'),'exec','demo-search','generate','{"model":"fixture-model","messages":[],"max_tokens":10}'],env=env,cwd=root,capture_output=True,text=True,timeout=20)
        data = json.loads(result.stdout)
        assert result.returncode != 0 and data['code'] == 'preflight_blocked' and data['blocked_at'] == 'authorization' and data['upstream_called'] is False
        assert Provider.generation_calls == before_generations+1
        api(f'/local/api/token-policies/{token_id}','DELETE')
        unavailable = subprocess.run([str(project/'tools/lr'),'find','model','--exact','absent-pack:fixture-model'],env=env,cwd=root,capture_output=True,text=True,timeout=20)
        assert unavailable.returncode != 0 and json.loads(unavailable.stdout)['failures'][0]['code'] == 'pack_not_found'
        Provider.invalid_catalog = True
        result = subprocess.run([str(project/'tools/lr'),'exec','demo-search','generate','{"model":"fixture-model","messages":[],"max_tokens":10}'],env=env,cwd=root,capture_output=True,text=True,timeout=20)
        assert result.returncode != 0 and json.loads(result.stdout)['failures'][0]['code'] == 'model_catalog_invalid_response'
        assert Provider.generation_calls == before_generations+1
        Provider.invalid_catalog = False
        Provider.fail_generation = True
        result = subprocess.run([str(project/'tools/lr'),'exec','demo-search','generate','{"model":"fixture-model","messages":[],"max_tokens":10}'],env=env,cwd=root,capture_output=True,text=True,timeout=20)
        assert result.returncode != 0 and Provider.generation_calls == before_generations+2
        # One human scope approval replaces manual Token transfer. The CLI owns
        # its private claim and credential, and resumes without printing either.
        original_session = env['LOCALROUTER_AGENT_SESSION']
        env['LOCALROUTER_AGENT_SESSION'] = 'enrollment-session'
        requested_policy = {'packs':['demo-search'],'operations':['demo-search.models','demo-search.generate'],'models':['fixture-model'],'daily_request_limit':20}
        enrolled = cli('identity','request','enrolled-fixture',json.dumps(requested_policy))
        assert enrolled['state'] == 'pending' and not enrolled['ready']
        assert 'token' not in enrolled and Provider.generation_calls == before_generations+2
        pending_claim = subprocess.run([str(project/'tools/lr'),'identity','claim'],env=env,cwd=root,capture_output=True,text=True,timeout=20)
        assert pending_claim.returncode != 0 and not json.loads(pending_claim.stdout)['ready']
        assert cli('identity','request','enrolled-fixture',json.dumps(requested_policy))['id'] == enrolled['id']
        approval = api('/local/api/identity-requests/'+enrolled['id']+'/decision','POST',{'digest':enrolled['digest'],'approve':True})['data']
        assert approval['state'] == 'approved' and 'key' not in approval
        Provider.fail_generation = False
        auto_claim = subprocess.run([str(project/'tools/lr'),'exec','demo-search','generate','{"model":"fixture-model","messages":[],"max_tokens":10,"stream":true}'],env=env,cwd=root,capture_output=True,text=True,timeout=20)
        assert auto_claim.returncode == 0 and auto_claim.stdout.endswith('data: [DONE]\n\n'), (auto_claim.stdout,auto_claim.stderr)
        delivered = cli('identity','claim')
        assert delivered['ready'] and 'token' not in delivered
        whoami = cli('init'); assert whoami['agent_code'] == 'enrolled-fixture'
        issued_id = whoami['token_id']
        delivered_again = cli('identity','claim'); assert delivered_again == delivered
        assert cli('init')['token_id'] == issued_id
        private_token = Path(whoami['token_file'])
        assert private_token.stat().st_mode & 0o777 == 0o600
        assert private_token.read_text().strip() not in json.dumps(delivered)
        Provider.fail_generation = False
        response = subprocess.run([str(project/'tools/lr'),'exec','demo-search','generate','{"model":"fixture-model","messages":[],"max_tokens":10,"stream":true}'],env=env,cwd=root,capture_output=True,text=True,timeout=20)
        assert response.returncode == 0 and response.stdout.endswith('data: [DONE]\n\n'), (response.stdout,response.stderr)
        denied = subprocess.run([str(project/'tools/lr'),'call','demo-search','search','{}','{}','{"q":"outside approved scope"}'],env=env,cwd=root,capture_output=True,text=True,timeout=20)
        assert denied.returncode != 0 and json.loads(denied.stdout)['code'] == 'token_policy_denied'
        expanded = dict(requested_policy,operations=['demo-search.models','demo-search.generate','demo-search.search'])
        scope_request = cli('identity','access',json.dumps(expanded))
        assert scope_request['state'] == 'pending' and scope_request['owner_token_id'] == issued_id
        api('/local/api/identity-requests/'+scope_request['id']+'/decision','POST',{'digest':scope_request['digest'],'approve':True})
        assert cli('identity','access-status',scope_request['id'])['state'] == 'approved'
        assert cli('init')['token_id'] == issued_id
        assert cli('call','demo-search','search','{}','{}','{"q":"now within approved scope"}')['state'] == 'succeeded'
        api(f'/local/api/tokens/{issued_id}','DELETE')
        revoked_claim = subprocess.run([str(project/'tools/lr'),'identity','claim'],env=env,cwd=root,capture_output=True,text=True,timeout=20)
        assert revoked_claim.returncode != 0 and private_token.read_text().strip() not in revoked_claim.stdout
        env['LOCALROUTER_AGENT_SESSION'] = original_session
        # Forgetting a binding never deletes or revokes its issued Token.
        assert cli('identity','forget')['token_deleted'] is False and token_path.exists()
    print('Service workspace CLI / session identity / automatic preparation / single-call streaming / structured failures passed', flush=True)
    if args.demo:
        proposal_input['connection']['definition'].update(id='demo-reference', name='参考资料服务')
        proposal_input['bundle']['members'] = [{'pack':'demo-search','operations':['search','status','models','generate']},{'pack':'demo-reference','operations':['search','status','models','generate']}]
        proposal_input['reason'] = '研究 Agent 已验证文档检索，现在申请把参考资料服务加入同一工具包。一次授权包含服务接入与明确的调用权限。'
        input_path.write_text(json.dumps(proposal_input))
        pending = payload(cli('setup','prepare','@'+str(input_path)))['proposal']
        env.update(XDG_DATA_HOME=str(root/'client-data'),LOCALROUTER_AGENT_SESSION='browser-enrollment')
        env.pop('LOCALROUTER_API_TOKEN_FILE',None)
        identity_request=cli('identity','request','browser-agent',json.dumps({'packs':['demo-search'],'operations':['demo-search.models','demo-search.generate'],'models':['fixture-model'],'daily_request_limit':50,'max_in_flight':2}))
        (root/'demo.json').write_text(json.dumps({'url':base+'/#tokens','pending_id':pending['id'],'identity_request':identity_request['id'],'gateway_pid':process.pid,'token_id':token_id}))
        print(json.dumps({'url':base+'/#setup','directory':str(root),'gateway_pid':process.pid}),flush=True)
        while process.poll() is None: time.sleep(1)
finally:
    process.terminate()
    try: process.wait(timeout=10)
    except subprocess.TimeoutExpired: process.kill(); process.wait()
    provider.shutdown(); log.close()
    if not args.demo:
        import shutil
        shutil.rmtree(root)
