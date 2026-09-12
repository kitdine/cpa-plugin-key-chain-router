package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// 422 is intentional: statusFromError classifies this as a local, non-health
// failure, so scheduler ownership problems never open the candidate circuit.
var errSchedulerTicketUnclaimed = errors.New("kcr scheduler did not claim execution ticket (routing control status 422); KCR is not the active CPA scheduler for this execution")

// issueExecutionTicketV8 creates a pin token whose lifetime is the enclosing
// host.model.execute[_stream] attempt, not the short pre-claim ticket TTL.
//
// This mirrors the lifecycle used by existing CPA pinning plugins: register a
// token before the nested host execution, let scheduler.pick mark it claimed,
// then atomically consume it when that host execution returns. Active execution
// tokens therefore cannot disappear because an unrelated request runs ticket
// cleanup while a long non-stream response is still in flight.
func issueExecutionTicketV8(authIndex, provider string) string {
	for attempt := 0; attempt < 4; attempt++ {
		var raw [18]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return ""
		}
		tok := hex.EncodeToString(raw[:])
		runtimeState.Lock()
		if runtimeState.tickets == nil {
			runtimeState.tickets = map[string]ticketRecord{}
		}
		if _, exists := runtimeState.tickets[tok]; !exists {
			runtimeState.tickets[tok] = ticketRecord{
				AuthIndex: authIndex,
				Provider:  provider,
				ExpiresAt: time.Date(9999, time.December, 31, 23, 59, 59, 0, time.UTC),
			}
			runtimeState.Unlock()
			return tok
		}
		runtimeState.Unlock()
	}
	return ""
}

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
