package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const directLiveIdentityPrefixV10 = "@live-id:"

func resolveAuthIDByIndex(authIndex, provider string) (string, error) {
	authIndex = strings.TrimSpace(authIndex)
	provider = strings.TrimSpace(provider)
	if authIndex == "" {
		return "", fmt.Errorf("auth index is empty")
	}
	if strings.HasPrefix(authIndex, directLiveIdentityPrefixV10) {
		id := strings.TrimSpace(strings.TrimPrefix(authIndex, directLiveIdentityPrefixV10))
		if id == "" {
			return "", fmt.Errorf("direct live identity is empty")
		}
		return id, nil
	}

	var hostErr error
	raw, err := callHost(methodHostAuthList, map[string]any{})
	if err != nil {
		hostErr = fmt.Errorf("host.auth.list: %w", err)
	} else {
		var resp hostAuthListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			hostErr = fmt.Errorf("decode host.auth.list: %w", err)
		} else if id := authIDFromEntries(resp.Files, authIndex, provider); id != "" {
			return id, nil
		}
	}

	legacyID, reconcileErr := resolveLegacySyntheticAuthIDV10(authIndex, provider)
	if reconcileErr != nil {
		return "", reconcileErr
	}
	if legacyID != "" {
		return legacyID, nil
	}
	if hostErr != nil {
		return "", hostErr
	}
	return "", fmt.Errorf("auth index %q is neither a host auth file nor an exactly reconcilable config API credential", authIndex)
}

func resolveLegacySyntheticAuthIDV10(staleAuthIndex, provider string) (string, error) {
	staleAuthIndex = strings.TrimSpace(staleAuthIndex)
	provider = strings.TrimSpace(provider)
	if staleAuthIndex == "" || provider == "" {
		return "", nil
	}
	runtimeState.RLock()
	configPath := strings.TrimSpace(runtimeState.configPath)
	runtimeState.RUnlock()
	if configPath == "" {
		return "", nil
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("read CPA config for exact identity reconciliation: %w", err)
	}
	ids := legacyCurrentRuntimeIDsV8(string(data), staleAuthIndex, provider)
	unique := ""
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if unique == "" {
			unique = id
			continue
		}
		if unique != id {
			return "", fmt.Errorf("legacy API auth index %q maps to multiple current CPA runtime identities", staleAuthIndex)
		}
	}
	return unique, nil
}

func liveIDForCandidateV10(c *PolicyCandidate) (string, error) {
	if c == nil {
		return "", fmt.Errorf("candidate is nil")
	}
	if id := strings.TrimSpace(c.AuthID); id != "" {
		return id, nil
	}
	idx := strings.TrimSpace(c.AuthIndex)
	if idx == "" {
		return "", fmt.Errorf("candidate %q has no credential identity", c.Name)
	}
	return resolveAuthIDByIndex(idx, c.Provider)
}

func resourcesWithExactIDsV10() []apiResource {
	_, resources, _ := currentEnvironment()
	out := append([]apiResource(nil), resources...)
	for i := range out {
		if strings.TrimSpace(out[i].AuthID) != "" || strings.TrimSpace(out[i].AuthIndex) == "" {
			continue
		}
		if id, err := resolveAuthIDByIndex(out[i].AuthIndex, out[i].Provider); err == nil {
			out[i].AuthID = strings.TrimSpace(id)
		}
	}
	return out
}

func exactResourceForCandidateV10(c *PolicyCandidate, resources []apiResource) *apiResource {
	if c == nil {
		return nil
	}
	liveID, _ := liveIDForCandidateV10(c)
	var matched *apiResource
	for i := range resources {
		r := &resources[i]
		if !strings.EqualFold(strings.TrimSpace(r.Provider), strings.TrimSpace(c.Provider)) {
			continue
		}
		idMatch := liveID != "" && strings.TrimSpace(r.AuthID) == strings.TrimSpace(liveID)
		idxMatch := strings.TrimSpace(c.AuthIndex) != "" && strings.TrimSpace(r.AuthIndex) == strings.TrimSpace(c.AuthIndex)
		if !idMatch && !idxMatch {
			continue
		}
		if matched != nil {
			return nil
		}
		matched = r
	}
	return matched
}

func authIDFromEntries(entries []hostAuthEntry, authIndex, provider string) string {
	authIndex = strings.TrimSpace(authIndex)
	provider = strings.TrimSpace(provider)
	if authIndex == "" {
		return ""
	}
	matchedID := ""
	for _, entry := range entries {
		if strings.TrimSpace(entry.AuthIndex) != authIndex || strings.TrimSpace(entry.ID) == "" {
			continue
		}
		entryProvider := strings.TrimSpace(entry.Provider)
		if entryProvider == "" {
			entryProvider = strings.TrimSpace(entry.Type)
		}
		if provider != "" && !strings.EqualFold(entryProvider, provider) {
			continue
		}
		id := strings.TrimSpace(entry.ID)
		if matchedID == "" {
			matchedID = id
			continue
		}
		if id != matchedID {
			return ""
		}
	}
	return matchedID
}

func schedulerCandidateEligible(candidates []any, authID, authIndex, provider string) bool {
	authID = strings.TrimSpace(authID)
	authIndex = strings.TrimSpace(authIndex)
	provider = strings.TrimSpace(provider)
	if authID == "" && authIndex == "" {
		return false
	}
	for _, raw := range candidates {
		m := anyMap(raw)
		id := stringAny(m, "ID")
		if id == "" {
			id = stringAny(m, "id")
		}
		if authID != "" {
			if strings.TrimSpace(id) != authID {
				continue
			}
		} else {
			idx := stringAny(m, "AuthIndex")
			if idx == "" {
				idx = stringAny(m, "auth_index")
			}
			if authIndex == "" || strings.TrimSpace(idx) != authIndex {
				continue
			}
		}
		candidateProvider := stringAny(m, "Provider")
		if candidateProvider == "" {
			candidateProvider = stringAny(m, "provider")
		}
		if provider != "" && !strings.EqualFold(strings.TrimSpace(candidateProvider), provider) {
			continue
		}
		return true
	}
	return false
}
