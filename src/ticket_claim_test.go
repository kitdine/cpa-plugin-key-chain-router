package main

import "testing"

func TestFinishExecutionTicketRequiresClaim(t *testing.T) {
	tok := issueTicket("idx-a", "codex")
	if finishExecutionTicket(tok) {
		t.Fatal("unclaimed execution ticket must fail closed")
	}
	if _, ok, _ := claimTicket(tok); ok {
		t.Fatal("finished ticket must be removed")
	}
}

func TestFinishExecutionTicketAcceptsClaimedTicket(t *testing.T) {
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

func TestFinishExecutionTicketWithoutPinIsNoop(t *testing.T) {
	if !finishExecutionTicket("") {
		t.Fatal("empty ticket should not require scheduler ownership")
	}
}
