package main

import (
	"encoding/json"
	"os"
	"strings"
)

type cpaCredentialDiagnosticV10 struct {
	Priority      int
	PriorityKnown bool
	Status        string
	Unavailable   bool
	RuntimeKnown  bool
	Source        string
}

func candidateCPADiagnosticV10(c *PolicyCandidate) cpaCredentialDiagnosticV10 {
	if c == nil {
		return cpaCredentialDiagnosticV10{}
	}
	liveID, _ := liveIDForCandidateV10(c)
	if liveID != "" {
		if raw, err := callHost(methodHostAuthList, map[string]any{}); err == nil {
			var resp hostAuthListResponse
			if json.Unmarshal(raw, &resp) == nil {
				var matched *hostAuthEntry
				for i := range resp.Files {
					a := &resp.Files[i]
					provider := strings.TrimSpace(a.Provider)
					if provider == "" {
						provider = strings.TrimSpace(a.Type)
					}
					if strings.TrimSpace(a.ID) != liveID || !strings.EqualFold(provider, strings.TrimSpace(c.Provider)) {
						continue
					}
					if matched != nil {
						return cpaCredentialDiagnosticV10{Source: "host-auth-ambiguous"}
					}
					matched = a
				}
				if matched != nil {
					return cpaCredentialDiagnosticV10{
						Priority: matched.Priority, PriorityKnown: true,
						Status: matched.Status, Unavailable: matched.Unavailable,
						RuntimeKnown: true, Source: "host.auth.list",
					}
				}
			}
		}
	}

	if priority, ok := configPriorityForCandidateV10(c); ok {
		return cpaCredentialDiagnosticV10{
			Priority: priority, PriorityKnown: true,
			Status:       "configured; runtime cooldown/status not exposed by host.auth.list",
			RuntimeKnown: false, Source: "config.yaml",
		}
	}
	return cpaCredentialDiagnosticV10{}
}

func configPriorityForCandidateV10(c *PolicyCandidate) (int, bool) {
	if c == nil {
		return 0, false
	}
	runtimeState.RLock()
	path := strings.TrimSpace(runtimeState.configPath)
	runtimeState.RUnlock()
	if path == "" {
		return 0, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	inv, err := readCPAConfigInventory(string(raw))
	if err != nil {
		return 0, false
	}
	return cpaPriorityFromInventory(inv, c)
}

func cpaPriorityFromInventory(inv cpaConfigInventory, c *PolicyCandidate) (int, bool) {
	provider, idx, id := strings.TrimSpace(c.Provider), strings.TrimSpace(c.AuthIndex), strings.TrimSpace(c.AuthID)
	if provider == "" || (idx == "" && id == "") {
		return 0, false
	}
	var priority int
	found := false
	for _, item := range inv.resources {
		if !strings.EqualFold(item.resource.Provider, provider) {
			continue
		}
		if (id != "" && item.liveID != id) || (id == "" && item.resource.AuthIndex != idx) {
			continue
		}
		if found {
			return 0, false
		}
		priority, found = item.priority, true
	}
	return priority, found
}
