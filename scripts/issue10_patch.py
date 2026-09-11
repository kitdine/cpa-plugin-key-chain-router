from pathlib import Path


def replace_once(path: str, old: str, new: str) -> None:
    p = Path(path)
    text = p.read_text()
    n = text.count(old)
    if n != 1:
        raise SystemExit(f"{path}: expected exactly one match, got {n}")
    p.write_text(text.replace(old, new, 1))


replace_once(
    "src/main.go",
    """type ticketRecord struct {
\tAuthIndex string
\tProvider  string
\tExpiresAt time.Time
}
""",
    """type ticketRecord struct {
\tAuthIndex string
\tProvider  string
\tExpiresAt time.Time
\tClaimed   bool
}
""",
)

replace_once(
    "src/main.go",
    """\trec, ok := consumeTicket(tok)
\tif !ok {
\t\treturn okEnvelope(map[string]any{\"Handled\": false})
\t}
\tprovider := strings.TrimSpace(rec.Provider)
\tif provider == \"\" {
\t\tprovider = stringAny(req, \"Provider\")
\t\tif provider == \"\" {
\t\t\tprovider = stringAny(req, \"provider\")
\t\t}
\t}
\tcandidates := anySlice(req[\"Candidates\"])
\tif len(candidates) == 0 {
\t\tcandidates = anySlice(req[\"candidates\"])
\t}
\tif !schedulerCandidateEligible(candidates, rec.AuthIndex, provider) {
\t\treturn okEnvelope(map[string]any{\"Handled\": true, \"AuthID\": \"\", \"Reason\": \"kcr_candidate_ineligible\"})
\t}
\tauthID, err := resolveAuthIDByIndex(rec.AuthIndex, provider)
\tif err != nil {
\t\treturn okEnvelope(map[string]any{\"Handled\": true, \"AuthID\": \"\", \"Reason\": \"kcr_auth_resolution_failed\"})
\t}
\treturn okEnvelope(map[string]any{\"Handled\": true, \"AuthID\": authID})
""",
    """\trec, ok, firstClaim := claimTicket(tok)
\tif !ok {
\t\treturn nil, errors.New(\"kcr ticket is invalid or expired; refusing CPA scheduler fallback\")
\t}
\tif !firstClaim {
\t\treturn nil, errors.New(\"kcr ticket was already claimed; refusing CPA credential fallback inside one KCR attempt\")
\t}
\tprovider := strings.TrimSpace(rec.Provider)
\tif provider == \"\" {
\t\tprovider = stringAny(req, \"Provider\")
\t\tif provider == \"\" {
\t\t\tprovider = stringAny(req, \"provider\")
\t\t}
\t}
\tauthID, err := resolveAuthIDByIndex(rec.AuthIndex, provider)
\tif err != nil {
\t\treturn nil, fmt.Errorf(\"kcr auth resolution failed: %w\", err)
\t}
\tcandidates := anySlice(req[\"Candidates\"])
\tif len(candidates) == 0 {
\t\tcandidates = anySlice(req[\"candidates\"])
\t}
\tif !schedulerCandidateEligible(candidates, authID, provider) {
\t\treturn nil, errors.New(\"kcr pinned credential is not eligible in the current CPA candidate set\")
\t}
\treturn okEnvelope(map[string]any{\"Handled\": true, \"AuthID\": authID})
""",
)

replace_once(
    "src/main.go",
    """func consumeTicket(tok string) (ticketRecord, bool) {
\truntimeState.Lock()
\tdefer runtimeState.Unlock()
\tcleanupTicketsLocked()
\tr, ok := runtimeState.tickets[tok]
\tif ok {
\t\tdelete(runtimeState.tickets, tok)
\t}
\treturn r, ok
}
""",
    """func claimTicket(tok string) (ticketRecord, bool, bool) {
\truntimeState.Lock()
\tdefer runtimeState.Unlock()
\tcleanupTicketsLocked()
\tr, ok := runtimeState.tickets[tok]
\tif !ok {
\t\treturn ticketRecord{}, false, false
\t}
\tif r.Claimed {
\t\treturn r, true, false
\t}
\tr.Claimed = true
\truntimeState.tickets[tok] = r
\treturn r, true, true
}
func revokeTicket(tok string) {
\ttok = strings.TrimSpace(tok)
\tif tok == \"\" {
\t\treturn
\t}
\truntimeState.Lock()
\tdelete(runtimeState.tickets, tok)
\truntimeState.Unlock()
}
""",
)

replace_once(
    "src/scheduler_auth.go",
    """func schedulerCandidateEligible(candidates []any, authIndex, provider string) bool {
\tauthIndex = strings.TrimSpace(authIndex)
\tprovider = strings.TrimSpace(provider)
\tif authIndex == \"\" {
\t\treturn false
\t}
\tfor _, raw := range candidates {
\t\tm := anyMap(raw)
\t\tidx := stringAny(m, \"AuthIndex\")
\t\tif idx == \"\" {
\t\t\tidx = stringAny(m, \"auth_index\")
\t\t}
\t\tif strings.TrimSpace(idx) != authIndex {
\t\t\tcontinue
\t\t}
\t\tcandidateProvider := stringAny(m, \"Provider\")
\t\tif candidateProvider == \"\" {
\t\t\tcandidateProvider = stringAny(m, \"provider\")
\t\t}
\t\tif provider != \"\" && !strings.EqualFold(strings.TrimSpace(candidateProvider), provider) {
\t\t\tcontinue
\t\t}
\t\treturn true
\t}
\treturn false
}
""",
    """func schedulerCandidateEligible(candidates []any, authID, provider string) bool {
\tauthID = strings.TrimSpace(authID)
\tprovider = strings.TrimSpace(provider)
\tif authID == \"\" {
\t\treturn false
\t}
\tfor _, raw := range candidates {
\t\tm := anyMap(raw)
\t\tid := stringAny(m, \"ID\")
\t\tif id == \"\" {
\t\t\tid = stringAny(m, \"id\")
\t\t}
\t\tif strings.TrimSpace(id) != authID {
\t\t\tcontinue
\t\t}
\t\tcandidateProvider := stringAny(m, \"Provider\")
\t\tif candidateProvider == \"\" {
\t\t\tcandidateProvider = stringAny(m, \"provider\")
\t\t}
\t\tif provider != \"\" && !strings.EqualFold(strings.TrimSpace(candidateProvider), provider) {
\t\t\tcontinue
\t\t}
\t\treturn true
\t}
\treturn false
}
""",
)

replace_once(
    "src/v4_execution.go",
    """\th := cloneHeader(headers)
\th.Del(ticketHeader)
\tif c.AuthIndex != \"\" {
\t\th.Set(ticketHeader, issueTicket(c.AuthIndex, c.Provider))
\t}
\tstarted := time.Now()
\tmethod := methodHostModelExecute
\tif stream {
\t\tmethod = methodHostModelExecuteStream
\t}
\traw, err := callHost(method, map[string]any{\"entry_protocol\": source, \"exit_protocol\": source, \"model\": model, \"stream\": stream, \"body\": rewriteBodyModel(body, model), \"headers\": h, \"query\": query, \"alt\": alt, \"host_callback_id\": callbackID})
""",
    """\th := cloneHeader(headers)
\th.Del(ticketHeader)
\tticket := \"\"
\tif c.AuthIndex != \"\" {
\t\tticket = issueTicket(c.AuthIndex, c.Provider)
\t\th.Set(ticketHeader, ticket)
\t}
\tstarted := time.Now()
\tmethod := methodHostModelExecute
\tif stream {
\t\tmethod = methodHostModelExecuteStream
\t}
\traw, err := callHost(method, map[string]any{\"entry_protocol\": source, \"exit_protocol\": source, \"model\": model, \"stream\": stream, \"body\": rewriteBodyModel(body, model), \"headers\": h, \"query\": query, \"alt\": alt, \"host_callback_id\": callbackID})
\trevokeTicket(ticket)
""",
)

replace_once(
    "src/v4_execution.go",
    """\t\th := cloneHeader(headers)
\t\th.Del(ticketHeader)
\t\tif c.AuthIndex != \"\" {
\t\t\th.Set(ticketHeader, issueTicket(c.AuthIndex, c.Provider))
\t\t}
\t\tt := time.Now()
\t\traw, err := callHost(methodHostModelExecuteStream, map[string]any{\"entry_protocol\": source, \"exit_protocol\": source, \"model\": model, \"stream\": true, \"body\": rewriteBodyModel(body, model), \"headers\": h, \"query\": query, \"alt\": alt, \"host_callback_id\": callbackID})
""",
    """\t\th := cloneHeader(headers)
\t\th.Del(ticketHeader)
\t\tticket := \"\"
\t\tif c.AuthIndex != \"\" {
\t\t\tticket = issueTicket(c.AuthIndex, c.Provider)
\t\t\th.Set(ticketHeader, ticket)
\t\t}
\t\tt := time.Now()
\t\traw, err := callHost(methodHostModelExecuteStream, map[string]any{\"entry_protocol\": source, \"exit_protocol\": source, \"model\": model, \"stream\": true, \"body\": rewriteBodyModel(body, model), \"headers\": h, \"query\": query, \"alt\": alt, \"host_callback_id\": callbackID})
\t\trevokeTicket(ticket)
""",
)

abi = Path("src/abi_smoke.py")
text = abi.read_text()
if text.count("logs=[]\n") != 1:
    raise SystemExit("abi_smoke.py: logs marker mismatch")
text = text.replace(
    "logs=[]\n",
    "logs=[]\nscheduler_second_pick_blocked=False\nscheduler_first_pick_count=0\n",
    1,
)
if text.count("    global captured_ticket, captured_model, output_closed, upread\n") != 1:
    raise SystemExit("abi_smoke.py: host_call global marker mismatch")
text = text.replace(
    "    global captured_ticket, captured_model, output_closed, upread\n",
    "    global captured_ticket, captured_model, output_closed, upread, scheduler_second_pick_blocked, scheduler_first_pick_count\n",
    1,
)
old_exec = """    if m=='host.model.execute':
        hs=payload.get('headers') or {}
        vals=hs.get('X-CPA-Key-Chain-Ticket') or hs.get('X-Cpa-Key-Chain-Ticket') or []
        if isinstance(vals,str): vals=[vals]
        captured_ticket=vals[0] if vals else None
        captured_model=payload.get('model')
        body=base64.b64encode(b'{\"ok\":true}').decode()
        return_bytes(out, env_ok({'status_code':200,'headers':{'Content-Type':['application/json']},'body':body})); return 0
"""
new_exec = """    if m=='host.model.execute':
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
        body=base64.b64encode(b'{\"ok\":true}').decode()
        return_bytes(out, env_ok({'status_code':200,'headers':{'Content-Type':['application/json']},'body':body})); return 0
"""
if text.count(old_exec) != 1:
    raise SystemExit(f"abi_smoke.py: execute block match count={text.count(old_exec)}")
text = text.replace(old_exec, new_exec, 1)
old_post = """    first_ticket=captured_ticket
    sch=pcall('scheduler.pick',{'Provider':'codex','Model':'gpt-anything','Options':{'Headers':{'X-CPA-Key-Chain-Ticket':[first_ticket]}},'Candidates':[{'id':'wrong-candidate-id','provider':'codex','auth_index':'9b725f538a48ad68'}]})
    assert sch['Handled'] is True and sch['AuthID']=='real-auth-target', sch
    sch2=pcall('scheduler.pick',{'Provider':'codex','Model':'gpt-anything','Options':{'Headers':{'X-CPA-Key-Chain-Ticket':[first_ticket]}},'Candidates':[{'id':'wrong-candidate-id','provider':'codex','auth_index':'9b725f538a48ad68'}]})
    assert sch2['Handled'] is False, sch2
"""
new_post = """    first_ticket=captured_ticket
    assert scheduler_first_pick_count >= 1, scheduler_first_pick_count
    assert scheduler_second_pick_blocked is True
    try:
        pcall('scheduler.pick',{'Provider':'codex','Model':'gpt-anything','Options':{'Headers':{'X-CPA-Key-Chain-Ticket':[first_ticket]}},'Candidates':[{'ID':'real-auth-target','Provider':'codex'}]})
    except RuntimeError:
        pass
    else:
        raise AssertionError('ticket must be revoked after host.model.execute returns')
"""
if text.count(old_post) != 1:
    raise SystemExit(f"abi_smoke.py: post block match count={text.count(old_post)}")
abi.write_text(text.replace(old_post, new_post, 1))

Path("src/issue10_test.go").write_text(
    r'''package main

import (
    "testing"
    "time"
)

func TestIssue10TicketCanOnlyBeClaimedOnceBeforeRevoke(t *testing.T) {
    runtimeState.Lock()
    oldTickets := runtimeState.tickets
    oldTTL := runtimeState.cfg.TicketTTL
    runtimeState.tickets = map[string]ticketRecord{}
    runtimeState.cfg.TicketTTL = time.Minute
    runtimeState.Unlock()
    defer func() {
        runtimeState.Lock()
        runtimeState.tickets = oldTickets
        runtimeState.cfg.TicketTTL = oldTTL
        runtimeState.Unlock()
    }()

    tok := issueTicket("idx-a", "codex")
    rec, ok, first := claimTicket(tok)
    if !ok || !first || rec.AuthIndex != "idx-a" || rec.Provider != "codex" {
        t.Fatalf("first claim = (%+v, %v, %v), want valid first claim", rec, ok, first)
    }
    if _, ok, first = claimTicket(tok); !ok || first {
        t.Fatalf("second claim = (ok=%v, first=%v), want existing but already claimed", ok, first)
    }
    revokeTicket(tok)
    if _, ok, _ = claimTicket(tok); ok {
        t.Fatal("revoked ticket must no longer be claimable")
    }
}

func TestIssue10SchedulerEligibilityUsesRuntimeAuthID(t *testing.T) {
    candidates := []any{
        map[string]any{"ID": "runtime-auth-a", "Provider": "codex"},
        map[string]any{"id": "runtime-auth-b", "provider": "claude"},
    }
    if !schedulerCandidateEligible(candidates, "runtime-auth-a", "codex") {
        t.Fatal("matching runtime AuthID must be eligible")
    }
    if schedulerCandidateEligible(candidates, "idx-a", "codex") {
        t.Fatal("stable AuthIndex must not be mistaken for CPA scheduler candidate ID")
    }
    if schedulerCandidateEligible(candidates, "runtime-auth-a", "claude") {
        t.Fatal("provider mismatch must be rejected")
    }
    if schedulerCandidateEligible(nil, "runtime-auth-a", "codex") {
        t.Fatal("empty candidate set must be rejected")
    }
}
'''
)
