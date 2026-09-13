package main

import "strings"

func issueExecutionTicketV10(liveID, authIndex, provider string) string {
	identity := strings.TrimSpace(authIndex)
	if liveID = strings.TrimSpace(liveID); liveID != "" {
		identity = directLiveIdentityPrefixV10 + liveID
	}
	return issueExecutionTicketV8(identity, provider)
}
