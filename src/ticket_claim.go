package main

import (
	"errors"
	"strings"
)

var errSchedulerTicketUnclaimed = errors.New("kcr scheduler did not claim execution ticket; KCR is not the active CPA scheduler for this execution")

// finishExecutionTicket consumes one execution ticket and reports whether KCR's
// scheduler actually claimed it. A successful host.model.execute result is not
// proof that the configured KCR candidate ran: CPA may have routed the nested
// request through another scheduler (or its built-in selector) without ever
// invoking KCR's scheduler.pick.
func finishExecutionTicket(tok string) bool {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return true
	}
	runtimeState.Lock()
	rec, ok := runtimeState.tickets[tok]
	delete(runtimeState.tickets, tok)
	runtimeState.Unlock()
	return ok && rec.Claimed
}
