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
