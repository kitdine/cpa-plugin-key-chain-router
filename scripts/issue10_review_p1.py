from pathlib import Path


def replace_once(path: str, old: str, new: str) -> None:
    p = Path(path)
    text = p.read_text()
    n = text.count(old)
    if n != 1:
        raise SystemExit(f"{path}: expected exactly one match, got {n}")
    p.write_text(text.replace(old, new, 1))


replace_once(
    "src/scheduler_auth.go",
    '''func schedulerCandidateEligible(candidates []any, authID, provider string) bool {
\tauthID = strings.TrimSpace(authID)
\tprovider = strings.TrimSpace(provider)
\tif authID == "" {
\t\treturn false
\t}
\tfor _, raw := range candidates {
\t\tm := anyMap(raw)
\t\tid := stringAny(m, "ID")
\t\tif id == "" {
\t\t\tid = stringAny(m, "id")
\t\t}
\t\tif strings.TrimSpace(id) != authID {
\t\t\tcontinue
\t\t}
\t\tcandidateProvider := stringAny(m, "Provider")
\t\tif candidateProvider == "" {
\t\t\tcandidateProvider = stringAny(m, "provider")
\t\t}
\t\tif provider != "" && !strings.EqualFold(strings.TrimSpace(candidateProvider), provider) {
\t\t\tcontinue
\t\t}
\t\treturn true
\t}
\treturn false
}
''',
    '''func schedulerCandidateEligible(candidates []any, authID, authIndex, provider string) bool {
\tauthID = strings.TrimSpace(authID)
\tauthIndex = strings.TrimSpace(authIndex)
\tprovider = strings.TrimSpace(provider)
\tif authID == "" && authIndex == "" {
\t\treturn false
\t}
\tfor _, raw := range candidates {
\t\tm := anyMap(raw)
\t\tid := stringAny(m, "ID")
\t\tif id == "" {
\t\t\tid = stringAny(m, "id")
\t\t}
\t\tidx := stringAny(m, "AuthIndex")
\t\tif idx == "" {
\t\t\tidx = stringAny(m, "auth_index")
\t\t}
\t\tidMatch := authID != "" && strings.TrimSpace(id) == authID
\t\tindexMatch := authIndex != "" && strings.TrimSpace(idx) == authIndex
\t\tif !idMatch && !indexMatch {
\t\t\tcontinue
\t\t}
\t\tcandidateProvider := stringAny(m, "Provider")
\t\tif candidateProvider == "" {
\t\t\tcandidateProvider = stringAny(m, "provider")
\t\t}
\t\tif provider != "" && !strings.EqualFold(strings.TrimSpace(candidateProvider), provider) {
\t\t\tcontinue
\t\t}
\t\treturn true
\t}
\treturn false
}
''',
)

replace_once(
    "src/main.go",
    '''\tif !schedulerCandidateEligible(candidates, authID, provider) {
\t\treturn nil, errors.New("kcr pinned credential is not eligible in the current CPA candidate set")
\t}
''',
    '''\tif !schedulerCandidateEligible(candidates, authID, rec.AuthIndex, provider) {
\t\treturn nil, errors.New("kcr pinned credential is not eligible in the current CPA candidate set")
\t}
''',
)

Path("src/issue10_test.go").write_text('''package main

import (
\t"testing"
\t"time"
)

func TestIssue10TicketCanOnlyBeClaimedOnceBeforeRevoke(t *testing.T) {
\truntimeState.Lock()
\toldTickets := runtimeState.tickets
\toldTTL := runtimeState.cfg.TicketTTL
\truntimeState.tickets = map[string]ticketRecord{}
\truntimeState.cfg.TicketTTL = time.Minute
\truntimeState.Unlock()
\tdefer func() {
\t\truntimeState.Lock()
\t\truntimeState.tickets = oldTickets
\t\truntimeState.cfg.TicketTTL = oldTTL
\t\truntimeState.Unlock()
\t}()

\ttok := issueTicket("idx-a", "codex")
\trec, ok, first := claimTicket(tok)
\tif !ok || !first || rec.AuthIndex != "idx-a" || rec.Provider != "codex" {
\t\tt.Fatalf("first claim = (%+v, %v, %v), want valid first claim", rec, ok, first)
\t}
\tif _, ok, first = claimTicket(tok); !ok || first {
\t\tt.Fatalf("second claim = (ok=%v, first=%v), want existing but already claimed", ok, first)
\t}
\trevokeTicket(tok)
\tif _, ok, _ = claimTicket(tok); ok {
\t\tt.Fatal("revoked ticket must no longer be claimable")
\t}
}

func TestIssue10SchedulerEligibilitySupportsCurrentAndLegacyIdentity(t *testing.T) {
\tcurrent := []any{
\t\tmap[string]any{"ID": "runtime-auth-a", "Provider": "codex"},
\t}
\tif !schedulerCandidateEligible(current, "runtime-auth-a", "idx-a", "codex") {
\t\tt.Fatal("matching runtime AuthID must be eligible")
\t}

\tlegacy := []any{
\t\tmap[string]any{"id": "wrong-candidate-id", "auth_index": "idx-a", "provider": "codex"},
\t}
\tif !schedulerCandidateEligible(legacy, "runtime-auth-a", "idx-a", "codex") {
\t\tt.Fatal("matching stable AuthIndex must preserve legacy candidate compatibility")
\t}
\tif schedulerCandidateEligible(legacy, "runtime-auth-b", "idx-b", "codex") {
\t\tt.Fatal("candidate must be rejected when neither AuthID nor AuthIndex matches")
\t}
\tif schedulerCandidateEligible(legacy, "runtime-auth-a", "idx-a", "claude") {
\t\tt.Fatal("provider mismatch must be rejected")
\t}
\tif schedulerCandidateEligible(nil, "runtime-auth-a", "idx-a", "codex") {
\t\tt.Fatal("empty candidate set must be rejected")
\t}
}
''')

p = Path("src/review_findings_test.go")
text = p.read_text()
start = text.index("func TestSchedulerCandidateEligibleRequiresMembership")
end = text.index("\nfunc TestAuthIDFromEntriesRejectsDuplicateProviderMatches", start)
replacement = '''func TestSchedulerCandidateEligibleRequiresMembership(t *testing.T) {
\tcandidates := []any{
\t\tmap[string]any{"id": "wrong-candidate-id", "auth_index": "idx-a", "provider": "codex"},
\t}
\tif !schedulerCandidateEligible(candidates, "runtime-auth-a", "idx-a", "codex") {
\t\tt.Fatal("expected matching stable AuthIndex scheduler candidate to be eligible")
\t}
\tif schedulerCandidateEligible(candidates, "runtime-auth-b", "idx-b", "codex") {
\t\tt.Fatal("candidate absent by both runtime AuthID and AuthIndex must be rejected")
\t}
\tif schedulerCandidateEligible(candidates, "runtime-auth-a", "idx-a", "claude") {
\t\tt.Fatal("provider-mismatched scheduler candidate must be rejected")
\t}
\tif schedulerCandidateEligible(nil, "runtime-auth-a", "idx-a", "codex") {
\t\tt.Fatal("empty Candidates must be rejected")
\t}
}
'''
p.write_text(text[:start] + replacement + text[end:])

p = Path("src/abi_smoke.py")
text = p.read_text()
text = text.replace("'Candidates':[{'ID':'real-auth-target','Provider':'codex'}]", "'Candidates':[{'ID':'wrong-candidate-id','Provider':'codex','AuthIndex':'9b725f538a48ad68'}]", 1)
text = text.replace("'Candidates':[{'ID':'real-auth-target','Provider':'codex'}]", "'Candidates':[{'ID':'wrong-candidate-id','Provider':'codex','AuthIndex':'9b725f538a48ad68'}]", 1)
p.write_text(text)
