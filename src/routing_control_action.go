package main

// executionFailureActionV8 applies FailoverPolicy only after KCR has retained
// control of the execution. If the execution ticket was never issued or never
// claimed by KCR's scheduler, continuing to another KCR candidate is unsafe:
// another scheduler may already have sent the nested request upstream, and no
// subsequent candidate result can be attributed reliably to KCR.
func executionFailureActionV8(f FailoverPolicy, status int, err error) string {
	if err == errSchedulerTicketUnclaimed || err == errSchedulerTicketIssue {
		return failStop
	}
	return failureActionV4(f, status, err)
}
