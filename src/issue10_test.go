package main

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

func TestIssue10SchedulerEligibilitySupportsCurrentAndLegacyIdentity(t *testing.T) {
	current := []any{
		map[string]any{"ID": "runtime-auth-a", "Provider": "codex"},
	}
	if !schedulerCandidateEligible(current, "runtime-auth-a", "idx-a", "codex") {
		t.Fatal("matching runtime AuthID must be eligible")
	}

	legacy := []any{
		map[string]any{"id": "wrong-candidate-id", "auth_index": "idx-a", "provider": "codex"},
	}
	if !schedulerCandidateEligible(legacy, "runtime-auth-a", "idx-a", "codex") {
		t.Fatal("matching stable AuthIndex must preserve legacy candidate compatibility")
	}
	if schedulerCandidateEligible(legacy, "runtime-auth-b", "idx-b", "codex") {
		t.Fatal("candidate must be rejected when neither AuthID nor AuthIndex matches")
	}
	if schedulerCandidateEligible(legacy, "runtime-auth-a", "idx-a", "claude") {
		t.Fatal("provider mismatch must be rejected")
	}
	if schedulerCandidateEligible(nil, "runtime-auth-a", "idx-a", "codex") {
		t.Fatal("empty candidate set must be rejected")
	}
}
