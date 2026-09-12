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
	tok := issueExecutionTicketV8("idx-a", "codex")
	if tok == "" {
		t.Fatal("failed to issue execution ticket")
	}
	if finishExecutionTicket(tok) {
		t.Fatal("unclaimed execution ticket must fail closed")
	}
	if _, ok, _ := claimTicket(tok); ok {
		t.Fatal("finished ticket must be removed")
	}
}

func TestFinishExecutionTicketAcceptsClaimedTicket(t *testing.T) {
	withTicketRuntimeForTest(t)
	tok := issueExecutionTicketV8("idx-a", "codex")
	if tok == "" {
		t.Fatal("failed to issue execution ticket")
	}
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

func TestExecutionTicketSurvivesConfiguredPreClaimTTL(t *testing.T) {
	withTicketRuntimeForTest(t)
	runtimeState.Lock()
	runtimeState.cfg.TicketTTL = 5 * time.Millisecond
	runtimeState.Unlock()

	tok := issueExecutionTicketV8("idx-a", "codex")
	if tok == "" {
		t.Fatal("failed to issue execution ticket")
	}
	time.Sleep(10 * time.Millisecond)

	// Legacy issueTicket still triggers cleanupTicketsLocked. An active execution
	// token is attempt-scoped and must survive that unrelated pre-claim cleanup.
	other := issueTicket("idx-b", "codex")
	defer revokeTicket(other)
	if _, ok, first := claimTicket(tok); !ok || !first {
		t.Fatalf("claimTicket() after configured TTL = (ok=%v, first=%v), want claimed", ok, first)
	}
	if !finishExecutionTicket(tok) {
		t.Fatal("active execution ticket was removed by pre-claim TTL cleanup")
	}
}

func TestFinishExecutionTicketWithoutPinIsNoop(t *testing.T) {
	if !finishExecutionTicket("") {
		t.Fatal("empty ticket should not require scheduler ownership")
	}
}
