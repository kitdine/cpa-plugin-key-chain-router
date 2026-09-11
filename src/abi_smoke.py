import base64, ctypes, hashlib, json, os, sys, tempfile, time
from ctypes import *

VERSION=os.environ.get('VERSION','').strip()
SO = os.path.abspath(sys.argv[1] if len(sys.argv) > 1 else ('../dist/key-chain-router-v'+VERSION+'.so' if VERSION else '../dist/key-chain-router.so'))
if not VERSION:
    name=os.path.basename(SO)
    prefix='key-chain-router-v'
    if name.startswith(prefix) and name.endswith('.so'):
        VERSION=name[len(prefix):-3]
lib = ctypes.CDLL(SO)

class Buffer(Structure):
    _fields_=[('ptr', c_void_p),('len', c_size_t)]
HOSTCALL=CFUNCTYPE(c_int,c_void_p,c_char_p,POINTER(c_uint8),c_size_t,POINTER(Buffer))
HOSTFREE=CFUNCTYPE(None,c_void_p,c_size_t)
PLUGINCALL=CFUNCTYPE(c_int,c_char_p,POINTER(c_uint8),c_size_t,POINTER(Buffer))
PLUGINFREE=CFUNCTYPE(None,c_void_p,c_size_t)
PLUGINSHUT=CFUNCTYPE(None)
class HostAPI(Structure):
    _fields_=[('abi_version',c_uint32),('host_ctx',c_void_p),('call',HOSTCALL),('free_buffer',HOSTFREE)]
class PluginAPI(Structure):
    _fields_=[('abi_version',c_uint32),('call',PLUGINCALL),('free_buffer',PLUGINFREE),('shutdown',PLUGINSHUT)]

allocs=[]
captured_ticket=None
captured_model=None
emitted=[]
output_closed=False
upread=0
logs=[]
scheduler_second_pick_blocked=False
scheduler_first_pick_count=0

def env_ok(result):
    return json.dumps({'ok':True,'result':result},separators=(',',':')).encode()

def return_bytes(out,b):
    buf=create_string_buffer(b)
    allocs.append(buf)
    out.contents.ptr=cast(buf,c_void_p)
    out.contents.len=len(b)

@HOSTCALL
def host_call(ctx, method, req, n, out):
    global captured_ticket, captured_model, output_closed, upread, scheduler_second_pick_blocked, scheduler_first_pick_count
    m=method.decode()
    raw=bytes((c_uint8*n).from_address(addressof(req.contents))) if req and n else b'{}'
    payload=json.loads(raw or b'{}')
    if m=='host.auth.list':
        return_bytes(out, env_ok({'files':[{'id':'oauth-a','auth_index':'1111222233334444','name':'a@example.com','provider':'codex','status':'active'},{'id':'real-auth-target','auth_index':'9b725f538a48ad68','name':'api@example.com','provider':'codex','status':'active'}]})); return 0
    if m=='host.log':
        logs.append(payload)
        return_bytes(out, env_ok({})); return 0
    if m=='host.model.execute_stream':
        hs=payload.get('headers') or {}
        vals=hs.get('X-CPA-Key-Chain-Ticket') or hs.get('X-Cpa-Key-Chain-Ticket') or []
        if isinstance(vals,str): vals=[vals]
        captured_ticket=vals[0] if vals else None
        captured_model=payload.get('model')
        return_bytes(out, env_ok({'status_code':200,'headers':{'Content-Type':['text/event-stream']},'stream_id':'upstream-1'})); return 0
    if m=='host.model.stream_read':
        upread += 1
        if upread == 1:
            return_bytes(out, env_ok({'payload':base64.b64encode(b'data: {"ok":true}\n\n').decode(),'done':False})); return 0
        return_bytes(out, env_ok({'done':True})); return 0
    if m=='host.model.stream_close':
        return_bytes(out, env_ok({})); return 0
    if m=='host.stream.emit':
        rawp=payload.get('payload') or ''
        emitted.append(base64.b64decode(rawp) if rawp else b'')
        return_bytes(out, env_ok({})); return 0
    if m=='host.stream.close':
        output_closed=True
        return_bytes(out, env_ok({})); return 0
    if m=='host.model.execute':
        hs=payload.get('headers') or {}
        vals=hs.get('X-CPA-Key-Chain-Ticket') or hs.get('X-Cpa-Key-Chain-Ticket') or []
        if isinstance(vals,str): vals=[vals]
        captured_ticket=vals[0] if vals else None
        captured_model=payload.get('model')
        if captured_ticket:
            req={'Provider':'codex','Model':captured_model,'Options':{'Headers':{'X-CPA-Key-Chain-Ticket':[captured_ticket]}},'Candidates':[{'ID':'real-auth-target','Provider':'codex'}]}
            sch=pcall('scheduler.pick',req)
            assert sch['Handled'] is True and sch['AuthID']=='real-auth-target', sch
            scheduler_first_pick_count += 1
            try:
                pcall('scheduler.pick',req)
            except RuntimeError:
                scheduler_second_pick_blocked=True
            else:
                raise AssertionError('second scheduler pick with the same KCR ticket must fail closed')
        body=base64.b64encode(b'{"ok":true}').decode()
        return_bytes(out, env_ok({'status_code':200,'headers':{'Content-Type':['application/json']},'body':body})); return 0
    return_bytes(out, json.dumps({'ok':False,'error':{'code':'unsupported','message':m}}).encode()); return 1

@HOSTFREE
def host_free(ptr,n):
    pass

host=HostAPI(1,None,host_call,host_free)
plugin=PluginAPI()
lib.cliproxy_plugin_init.argtypes=[POINTER(HostAPI),POINTER(PluginAPI)]
lib.cliproxy_plugin_init.restype=c_int
assert lib.cliproxy_plugin_init(byref(host),byref(plugin))==0
assert plugin.abi_version==1

def pcall(method,obj):
    raw=json.dumps(obj,separators=(',',':')).encode()
    arr=(c_uint8*len(raw)).from_buffer_copy(raw) if raw else None
    out=Buffer()
    rc=plugin.call(method.encode(),arr,len(raw),byref(out))
    data=string_at(out.ptr,out.len) if out.ptr and out.len else b''
    if out.ptr: plugin.free_buffer(out.ptr,out.len)
    if not data: raise RuntimeError((method,rc,'empty'))
    env=json.loads(data)
    if not env.get('ok'): raise RuntimeError((method,rc,env))
    return env['result']

with tempfile.TemporaryDirectory() as td:
    cfg_path=os.path.join(td,'config.yaml')
    state_path=os.path.join(td,'state.json')
    key='sk-down-xyz'
    fp=hashlib.sha256(key.encode()).hexdigest()
    open(cfg_path,'w').write('''api-keys:\n  - sk-down-xyz\ncodex-api-key:\n  - api-key: sk-up\n    base-url: https://up.example/v1\n    prefix: plus\n    models:\n      - name: gpt-5.6-luna\n        alias: luna\n''')
    # Feed a v0.3 state to exercise automatic migration into one v0.4 Policy.
    open(state_path,'w').write(json.dumps({'version':3,'routes':{'r1':{'id':'r1','name':'test','key_fingerprint':fp,'key_hint':'sk-d…-xyz','enabled':True,'match_models':['*'],'candidates':[{'id':'c1','name':'codex-api','resource_id':'api:codex:x:0','resource_kind':'API','provider':'codex','auth_index':'9b725f538a48ad68','override_model':'','enabled':True}]}}}))
    y=f'enabled: true\nstate_file: {state_path}\ncpa_config_file: {cfg_path}\nticket_ttl_seconds: 30\n'
    encoded=base64.b64encode(y.encode()).decode()
    reg=pcall('plugin.register',{'config_yaml':encoded,'schema_version':5})
    assert reg['schema_version']==5
    reg6=pcall('plugin.reconfigure',{'config_yaml':encoded,'schema_version':6})
    assert reg6['schema_version']==5
    reg1=pcall('plugin.reconfigure',{'config_yaml':encoded,'schema_version':1})
    assert reg1['schema_version']==1
    reg=pcall('plugin.reconfigure',{'config_yaml':encoded,'schema_version':5})
    assert reg['metadata']['Name']=='Key Chain Router'
    assert reg['metadata']['Version'], reg
    if VERSION:
        assert reg['metadata']['Version']==VERSION, (reg['metadata']['Version'], VERSION)
    assert reg['capabilities']['model_router'] and reg['capabilities']['scheduler'] and reg['capabilities']['executor']

    mr=pcall('management.register',{})
    assert any(x['Path']=='/status' for x in mr['resources'])
    snap=pcall('management.handle',{'Method':'GET','Path':'/v0/resource/plugins/key-chain-router/api','Query':{'action':['snapshot']},'Headers':{},'Body':''})
    snapbody=json.loads(base64.b64decode(snap['Body']))
    assert len(snapbody['downstream_keys'])==1
    assert len(snapbody['policies'])==1, snapbody
    assert snapbody['policies'][0]['key_fingerprint']==fp
    assert snapbody['downstream_keys'][0]['hint'] and 'sk-down-xyz' not in json.dumps(snapbody)
    obs=snapbody['observability']
    assert obs['memory_enabled'] is True and obs['log_enabled'] is True
    assert obs['sqlite_enabled'] is False and obs['response_headers'] is False

    route=pcall('model.route',{'RequestedModel':'gpt-anything','SourceFormat':'openai-response','Headers':{'Authorization':['Bearer '+key]},'Body':base64.b64encode(b'{"model":"gpt-anything"}').decode(),'Stream':False,'host_callback_id':'route-cb'})
    assert route['Handled'] is True and route['TargetKind']=='self', route

    ex=pcall('executor.execute',{'Model':'gpt-anything','SourceFormat':'openai-response','Headers':{'Authorization':['Bearer '+key]},'OriginalRequest':base64.b64encode(b'{"model":"gpt-anything","input":"hi"}').decode(),'Payload':base64.b64encode(b'{"model":"gpt-anything","input":"hi"}').decode(),'Query':{},'Metadata':{},'host_callback_id':'cb1'})
    assert captured_ticket, 'executor did not issue ticket'
    assert captured_model=='gpt-anything', captured_model
    # Debug response headers are off by default.
    assert not any(k.lower().startswith('x-kcr-') for k in (ex.get('Headers') or {}).keys())

    first_ticket=captured_ticket
    assert scheduler_first_pick_count >= 1, scheduler_first_pick_count
    assert scheduler_second_pick_blocked is True
    try:
        pcall('scheduler.pick',{'Provider':'codex','Model':'gpt-anything','Options':{'Headers':{'X-CPA-Key-Chain-Ticket':[first_ticket]}},'Candidates':[{'ID':'real-auth-target','Provider':'codex'}]})
    except RuntimeError:
        pass
    else:
        raise AssertionError('ticket must be revoked after host.model.execute returns')

    diag=pcall('management.handle',{'Method':'GET','Path':'/v0/resource/plugins/key-chain-router/api','Query':{'action':['diagnose'],'fingerprint':[fp],'model':['gpt-anything']},'Headers':{},'Body':''})
    diagbody=json.loads(base64.b64decode(diag['Body']))
    assert diagbody['matched'] is True
    assert diagbody['decision']=='KCR_HANDLED'
    assert diagbody['strategy']=='ordered-failover'

    snap=pcall('management.handle',{'Method':'GET','Path':'/v0/resource/plugins/key-chain-router/api','Query':{'action':['snapshot']},'Headers':{},'Body':''})
    snapbody=json.loads(base64.b64decode(snap['Body']))
    assert snapbody['recent_events'], 'no routing event recorded'
    ev=snapbody['recent_events'][0]
    assert ev['decision']=='KCR_HANDLED' and ev['success'] is True, ev
    assert ev['attempts'][0]['model']=='gpt-anything'
    assert any(x.get('message')=='kcr routing decision' for x in logs), logs

    emitted.clear(); output_closed=False; upread=0
    sx=pcall('executor.execute_stream',{'Model':'gpt-anything','SourceFormat':'openai-response','Headers':{'Authorization':['Bearer '+key]},'OriginalRequest':base64.b64encode(b'{"model":"gpt-anything","input":"hi","stream":true}').decode(),'Payload':base64.b64encode(b'{"model":"gpt-anything","input":"hi","stream":true}').decode(),'Query':{},'Metadata':{},'stream_id':'plugin-out-1','host_callback_id':'cb-stream'})
    deadline=time.time()+2
    while not output_closed and time.time()<deadline: time.sleep(0.01)
    assert output_closed, 'plugin output stream was not closed'
    assert emitted and b'data:' in emitted[0], emitted

plugin.shutdown()
print('ABI_SMOKE_PASS')
