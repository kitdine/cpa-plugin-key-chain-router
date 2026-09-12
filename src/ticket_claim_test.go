package main

import (
	"testing"
	"time"
)

func withTicketRuntimeForTest(t *testing.T) {
	t.Helper()
	runtimeState.Lock()
	oldTickets := runtimeState.tickets
	oldTTL := runtimeState.cfg.TicketTTL
	runtimeState.tickets = map[string]ticketRecord{}
	runtimeState.cfg.TicketTTL = time.Minute
	runtimeState.Unlock()
	t.Cleanup(func() {
		runtimeState.Lock()
		runtimeState.tickets = oldTickets
		runtimeState.cfg.TicketTTL = oldTTL
		runtimeState.Unlock()
	})
}

func TestFinishExecutionTicketRequiresClaim(t *testing.T) {
	withTicketRuntimeForTest(t)
	tok := issueTicket("idx-a", "codex")
	if finishExecutionTicket(tok) {
		t.Fatal("unclaimed execution ticket must fail closed")
	}
	if _, ok, _ := claimTicket(tok); ok {
		t.Fatal("finished ticket must be removed")
	}
}

func TestFinishExecutionTicketAcceptsClaimedTicket(t *testing.T) {
	withTicketRuntimeForTest(t)
	tok := issueTicket("idx-a", "codex")
	rec, ok, first := claimTicket(tok)
	if !ok || !first || rec.AuthIndex != "idx-a" {
		t.Fatalf("claimTicket() = (%#v, %v, %v)", rec, ok, first)
	}
	if !finishExecutionTicket(tok) {
		t.Fatal("claimed execution ticket must be accepted")
	}
	if _, ok, _ := claimTicket(tok); ok {
		t.Fatal("finished ticket must be removed")
	}
}

func TestClaimedExecutionTicketSurvivesPreClaimTTL(t *testing.T) {
	withTicketRuntimeForTest(t)
	runtimeState.Lock()
	runtimeState.cfg.TicketTTL = 5 * time.Millisecond
	runtimeState.Unlock()

	tok := issueTicket("idx-a", "codex")
	if _, ok, first := claimTicket(tok); !ok || !first {
		t.Fatalf("claimTicket()=(ok=%v, first=%v), want claimed", ok, first)
	}
	preserveClaimedTicketsV8()
	time.Sleep(10 * time.Millisecond)

	// issueTicket triggers cleanupTicketsLocked. An active claimed ticket must
	// survive that cleanup until its executor finalizes it.
	other := issueTicket("idx-b", "codex")
	defer revokeTicket(other)
	if !finishExecutionTicket(tok) {
		t.Fatal("claimed ticket was removed by pre-claim TTL cleanup")
	}
}

func TestFinishExecutionTicketWithoutPinIsNoop(t *testing.T) {
	if !finishExecutionTicket("") {
		t.Fatal("empty ticket should not require scheduler ownership")
	}
}
