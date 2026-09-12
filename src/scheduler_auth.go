package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// resolveAuthIDByIndex resolves the exact runtime AuthID KCR expects CPA to
// select. OAuth/file credentials are authoritative in host.auth.list. Config
// API-key credentials are different: current CPA keeps them as in-memory
// AuthManager records that are not necessarily exposed by host.auth.list, so a
// legacy KCR synthetic AuthIndex is bridged by reconstructing CPA's exact
// current StableID from config.yaml. The caller must still require that returned
// AuthID to exist in the live SchedulerPickRequest.Candidates set.
func resolveAuthIDByIndex(authIndex, provider string) (string, error) {
	authIndex = strings.TrimSpace(authIndex)
	provider = strings.TrimSpace(provider)
	if authIndex == "" {
		return "", fmt.Errorf("auth index is empty")
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

	// KCR <= v0.6.7 synthesized API-provider AuthIndex values from config.yaml.
	// Reconstruct the exact current CPA Auth.ID here; handleSchedulerPick then
	// proves that ID is live and selectable by matching it against req.Candidates.
	legacyID, reconcileErr := resolveLegacySyntheticAuthIDV8(authIndex, provider)
	if reconcileErr != nil {
		return "", reconcileErr
	}
	if legacyID != "" {
		return legacyID, nil
	}
	if hostErr != nil {
		return "", hostErr
	}
	return "", fmt.Errorf("auth index %q is neither a host auth file nor a reconcilable config API credential", authIndex)
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

		// Current CPA SchedulerAuthCandidate exposes the authoritative live Auth.ID
		// but not AuthIndex. Once an AuthID has been resolved, only exact ID
		// membership proves that this is the credential KCR intended to pin.
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
