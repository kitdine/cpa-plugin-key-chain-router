from pathlib import Path

p = Path('src/review_findings_test.go')
text = p.read_text()
old = '''func TestSchedulerCandidateEligibleRequiresMembership(t *testing.T) {
    candidates := []any{
        map[string]any{"auth_index": "idx-a", "provider": "codex"},
    }
    if !schedulerCandidateEligible(candidates, "idx-a", "codex") {
        t.Fatal("expected matching scheduler candidate to be eligible")
    }
    if schedulerCandidateEligible(candidates, "idx-b", "codex") {
        t.Fatal("ticketed auth index absent from Candidates must be rejected")
    }
    if schedulerCandidateEligible(candidates, "idx-a", "claude") {
        t.Fatal("provider-mismatched scheduler candidate must be rejected")
    }
    if schedulerCandidateEligible(nil, "idx-a", "codex") {
        t.Fatal("empty Candidates must be rejected")
    }
}
'''
new = '''func TestSchedulerCandidateEligibleRequiresMembership(t *testing.T) {
    candidates := []any{
        map[string]any{"id": "runtime-auth-a", "provider": "codex"},
    }
    if !schedulerCandidateEligible(candidates, "runtime-auth-a", "codex") {
        t.Fatal("expected matching runtime AuthID scheduler candidate to be eligible")
    }
    if schedulerCandidateEligible(candidates, "runtime-auth-b", "codex") {
        t.Fatal("runtime AuthID absent from Candidates must be rejected")
    }
    if schedulerCandidateEligible(candidates, "runtime-auth-a", "claude") {
        t.Fatal("provider-mismatched scheduler candidate must be rejected")
    }
    if schedulerCandidateEligible(nil, "runtime-auth-a", "codex") {
        t.Fatal("empty Candidates must be rejected")
    }
}
'''
if text.count(old) != 1:
    raise SystemExit(f'expected one legacy eligibility test, got {text.count(old)}')
p.write_text(text.replace(old, new, 1))
