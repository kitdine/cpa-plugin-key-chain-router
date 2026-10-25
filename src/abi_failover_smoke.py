"""ABI regression: quota/unavailable provider A must fall back to provider B.

This uses a minimal CPA host callback and no real upstream credentials.
Each case has a separate policy rule to isolate health/cooldown state.
"""
import base64
import ctypes
import hashlib
import json
import os
import sys
import tempfile
import time
from ctypes import *

so = os.path.abspath(sys.argv[1])
lib = ctypes.CDLL(so)
AUTH_A, AUTH_B = "codex-live-a", "gemini-live-b"
KEY = "sk-kcr-failover-test"
FINGERPRINT = hashlib.sha256(KEY.encode()).hexdigest()

class Buffer(Structure):
    _fields_ = [("ptr", c_void_p), ("len", c_size_t)]

HOSTCALL = CFUNCTYPE(c_int, c_void_p, c_char_p, POINTER(c_uint8), c_size_t, POINTER(Buffer))
HOSTFREE = CFUNCTYPE(None, c_void_p, c_size_t)
PLUGINCALL = CFUNCTYPE(c_int, c_char_p, POINTER(c_uint8), c_size_t, POINTER(Buffer))
PLUGINFREE = CFUNCTYPE(None, c_void_p, c_size_t)
PLUGINSHUT = CFUNCTYPE(None)

class HostAPI(Structure):
    _fields_ = [("abi_version", c_uint32), ("host_ctx", c_void_p),
                ("call", HOSTCALL), ("free_buffer", HOSTFREE)]

class PluginAPI(Structure):
    _fields_ = [("abi_version", c_uint32), ("call", PLUGINCALL),
                ("free_buffer", PLUGINFREE), ("shutdown", PLUGINSHUT)]

allocations = []
attempts = {}
emitted = []
closed = {}
reads = {}

def envelope(result):
    return json.dumps({"ok": True, "result": result}).encode()

def host_error(message, status):
    return json.dumps({"ok": False, "error": {
        "code": "host_call_failed", "message": message, "http_status": status
    }}).encode()

def write_buffer(out, data):
    buf = create_string_buffer(data)
    allocations.append(buf)
    out.contents.ptr = cast(buf, c_void_p)
    out.contents.len = len(data)

def invoke(method, obj):
    data = json.dumps(obj).encode()
    buf = (c_uint8 * len(data)).from_buffer_copy(data)
    out = Buffer()
    rc = plugin.call(method.encode(), buf, len(data), byref(out))
    result = string_at(out.ptr, out.len) if out.ptr and out.len else b""
    if out.ptr:
        plugin.free_buffer(out.ptr, out.len)
    if not result:
        raise AssertionError((method, rc, "empty plugin result"))
    decoded = json.loads(result)
    if not decoded.get("ok"):
        raise RuntimeError((method, rc, decoded))
    return decoded["result"]

def claim(payload):
    header = payload.get("headers") or {}
    ticket = (header.get("X-Cpa-Key-Chain-Ticket") or
              header.get("X-CPA-Key-Chain-Ticket") or [""])[0]
    assert ticket, payload
    auth_id = payload["auth_id"]
    provider = payload["forced_provider"]
    picked = invoke("scheduler.pick", {
        "Provider": provider, "Model": payload["model"],
        "Options": {"Headers": {"X-CPA-Key-Chain-Ticket": [ticket]}},
        "Candidates": [{"ID": auth_id, "Provider": provider, "Status": "active"}],
    })
    assert picked["Handled"] and picked["AuthID"] == auth_id, picked

@HOSTCALL
def host_call(ctx, method, req, n, out):
    name = method.decode()
    raw = bytes((c_uint8 * n).from_address(addressof(req.contents))) if req and n else b"{}"
    payload = json.loads(raw)
    if name == "host.auth.list":
        write_buffer(out, envelope({"files": [
            {"id": AUTH_A, "auth_index": "index-a", "provider": "codex", "status": "active", "name": "a.json"},
            {"id": AUTH_B, "auth_index": "index-b", "provider": "gemini", "status": "active", "name": "b.json"},
        ]}))
        return 0
    if name == "host.auth.get":
        write_buffer(out, envelope({"auth_index": payload.get("auth_index", ""), "json": {"prefix": ""}}))
        return 0
    if name == "host.log":
        write_buffer(out, envelope({}))
        return 0
    if name in ("host.model.execute", "host.model.execute_stream"):
        model = payload["model"]
        auth = payload["auth_id"]
        provider = payload["forced_provider"]
        assert (auth, provider) in ((AUTH_A, "codex"), (AUTH_B, "gemini")), payload
        attempts.setdefault(model, []).append(auth)
        if auth == AUTH_A:
            if model in ("gpt-quota", "gpt-stream-quota"):
                msg = json.dumps({"error": {
                    "code": "model_cooldown", "message": "All credentials for model are cooling down",
                    "provider": "codex", "reset_seconds": 3600,
                    "last_upstream_error": "quota exceeded"
                }})
                write_buffer(out, host_error(msg, 429))
                return 1
            if model == "gpt-auth-unavailable":
                write_buffer(out, host_error(
                    "auth_unavailable: no auth available (providers=codex, model=gpt-auth-unavailable; last upstream error: quota exhausted)",
                    503))
                return 1
            if model == "gpt-no-provider":
                write_buffer(out, host_error("provider_not_found: no provider supplied", 503))
                return 1
            if model == "gpt-unclaimed-upstream":
                write_buffer(out, host_error("simulated upstream HTTP 503", 503))
                return 1
        # Only a successfully selected/executed credential claims the scheduler ticket.
        claim(payload)
        if name == "host.model.execute_stream":
            stream_id = "stream-" + model
            reads[stream_id] = 0
            write_buffer(out, envelope({"status_code": 200, "stream_id": stream_id, "headers": {}}))
            return 0
        if auth == AUTH_A and model == "gpt-claimed-429":
            write_buffer(out, envelope({"status_code": 429, "headers": {}, "body": base64.b64encode(b'{"error":"quota"}').decode()}))
            return 0
        write_buffer(out, envelope({"status_code": 200, "headers": {},
                                    "body": base64.b64encode(b'{"ok":true}').decode()}))
        return 0
    if name == "host.model.stream_read":
        sid = payload["stream_id"]
        reads[sid] += 1
        if reads[sid] == 1:
            write_buffer(out, envelope({"payload": base64.b64encode(b'data: {"ok":true}\\n\\n').decode(), "done": False}))
        else:
            write_buffer(out, envelope({"done": True}))
        return 0
    if name == "host.model.stream_close":
        write_buffer(out, envelope({}))
        return 0
    if name == "host.stream.emit":
        emitted.append(base64.b64decode(payload.get("payload") or ""))
        write_buffer(out, envelope({}))
        return 0
    if name == "host.stream.close":
        closed[payload["stream_id"]] = payload.get("error", "")
        write_buffer(out, envelope({}))
        return 0
    write_buffer(out, host_error("unsupported host method " + name, 500))
    return 1

@HOSTFREE
def host_free(ptr, n):
    pass

host = HostAPI(1, None, host_call, host_free)
plugin = PluginAPI()
lib.cliproxy_plugin_init.argtypes = [POINTER(HostAPI), POINTER(PluginAPI)]
lib.cliproxy_plugin_init.restype = c_int
assert lib.cliproxy_plugin_init(byref(host), byref(plugin)) == 0
assert plugin.abi_version == 1

def make_rule(model):
    return {
        "id": "rule-" + model, "name": model, "models": [model],
        "strategy": "ordered-failover", "failover": {
            "network": "next", "rate_limit": "next", "server_error": "next", "exhausted": "error"
        },
        "candidates": [
            {"id": "a-" + model, "name": "A", "provider": "codex",
             "auth_id": AUTH_A, "auth_index": "index-a", "resource_kind": "OAuth",
             "enabled": True, "priority": 100, "weight": 1},
            {"id": "b-" + model, "name": "B", "provider": "gemini",
             "auth_id": AUTH_B, "auth_index": "index-b", "resource_kind": "OAuth",
             "enabled": True, "priority": 90, "weight": 1},
        ],
    }

models = ["gpt-quota", "gpt-auth-unavailable", "gpt-no-provider",
          "gpt-claimed-429", "gpt-unclaimed-upstream", "gpt-stream-quota"]

with tempfile.TemporaryDirectory() as directory:
    state_file = os.path.join(directory, "state.json")
    cpa_file = os.path.join(directory, "config.yaml")
    with open(cpa_file, "w", encoding="utf-8") as f:
        f.write("api-keys:\\n  - " + KEY + "\\n")
    state = {"version": 4, "policies": {
        FINGERPRINT: {
            "name": "A to B failover", "key_fingerprint": FINGERPRINT,
            "key_hint": "sk-kcr...", "enabled": True,
            "client_affinity": "off",
            "rules": [make_rule(model) for model in models],
        }
    }, "observability": {"memory_enabled": True, "memory_limit": 100,
                         "log_enabled": False, "sqlite_enabled": False}}
    with open(state_file, "w", encoding="utf-8") as f:
        json.dump(state, f)
    config_yaml = f"enabled: true\\nstate_file: {state_file}\\ncpa_config_file: {cpa_file}\\n"
    invoke("plugin.register", {"config_yaml": base64.b64encode(config_yaml.encode()).decode(),
                               "schema_version": 5})

    def execute(model, stream=False):
        body = json.dumps({"model": model, "input": "test", "stream": stream}).encode()
        args = {
            "Model": model, "SourceFormat": "openai-response",
            "Headers": {"Authorization": ["Bearer " + KEY]},
            "OriginalRequest": base64.b64encode(body).decode(),
            "Payload": base64.b64encode(body).decode(),
            "Query": {}, "Metadata": {}, "host_callback_id": "cb-" + model,
        }
        if stream:
            args["stream_id"] = "out-" + model
            invoke("executor.execute_stream", args)
            deadline = time.time() + 5
            while args["stream_id"] not in closed and time.time() < deadline:
                time.sleep(0.01)
            assert args["stream_id"] in closed, "stream never finished"
            assert closed[args["stream_id"]] == "", closed
        else:
            result = invoke("executor.execute", args)
            assert json.loads(base64.b64decode(result["Payload"]))["ok"] is True, result

    for model in models:
        if model == "gpt-unclaimed-upstream":
            try:
                execute(model)
            except RuntimeError as err:
                assert "scheduler did not claim execution ticket" in str(err), err
            else:
                raise AssertionError("unclaimed upstream 503 should remain terminal")
            assert attempts[model] == [AUTH_A], attempts[model]
            continue
        execute(model, stream=(model == "gpt-stream-quota"))
        assert attempts[model] == [AUTH_A, AUTH_B], (model, attempts[model])

    snapshot = invoke("management.handle", {
        "Method": "GET", "Path": "/v0/resource/plugins/key-chain-router/api",
        "Query": {"action": ["snapshot"]}, "Headers": {}, "Body": ""
    })
    events = json.loads(base64.b64decode(snapshot["Body"]))["recent_events"]
    for model in models:
        matching = [e for e in events if e.get("model") == model]
        assert matching, (model, events)
        ev = matching[0]
        if model == "gpt-unclaimed-upstream":
            assert not ev["success"] and len(ev["attempts"]) == 1, ev
        else:
            assert ev["success"] and ev["final"] == "B", ev
            assert [a["candidate"] for a in ev["attempts"]] == ["A", "B"], ev
    assert emitted and b"data:" in b"".join(emitted), emitted

plugin.shutdown()
print("ABI_FAILOVER_SMOKE_PASS")
