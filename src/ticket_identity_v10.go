package main

import "strings"

func ticketIdentityForCandidateV10(c *PolicyCandidate) string {
	if c == nil {
		return ""
	}
	if id := strings.TrimSpace(c.AuthID); id != "" {
		return directLiveIdentityPrefixV10 + id
	}
	return strings.TrimSpace(c.AuthIndex)
}

func issueCandidateExecutionTicketV10(c *PolicyCandidate) string {
	if c == nil {
		return ""
	}
	identity := ticketIdentityForCandidateV10(c)
	if identity == "" {
		return ""
	}
	return issueExecutionTicketV8(identity, c.Provider)
}
