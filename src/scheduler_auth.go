package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// resolveAuthIDByIndex resolves the runtime AuthID from CPA's authoritative
// host.auth.list data. AuthIndex is persisted by KCR because AuthID may change
// across host reloads/upgrades and must not be inferred from scheduler candidates.
func resolveAuthIDByIndex(authIndex, provider string) (string, error) {
	authIndex = strings.TrimSpace(authIndex)
	if authIndex == "" {
		return "", fmt.Errorf("auth index is empty")
	}

	raw, err := callHost(methodHostAuthList, map[string]any{})
	if err != nil {
		return "", fmt.Errorf("host.auth.list: %w", err)
	}
	var resp hostAuthListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("decode host.auth.list: %w", err)
	}

	id := authIDFromEntries(resp.Files, authIndex, provider)
	if id != "" {
		return id, nil
	}

	// KCR <= v0.6.7 synthesized API-provider AuthIndex values from config.yaml.
	// Current CPA owns identity in its live AuthManager and uses a different
	// StableID seed, so bridge an existing policy only when config metadata and
	// live runtime auth identify exactly one credential.
	legacyID, _, reconcileErr := resolveLegacySyntheticAuthV8(raw, authIndex, provider)
	if reconcileErr != nil {
		return "", reconcileErr
	}
	if legacyID != "" {
		return legacyID, nil
	}
	return "", fmt.Errorf("auth index %q not found in host.auth.list", authIndex)
}

func authIDFromEntries(entries []hostAuthEntry, authIndex, provider string) string {
	authIndex = strings.TrimSpace(authIndex)
	provider = strings.TrimSpace(provider)
	if authIndex == "" {
		return ""
	}

	matches := make([]hostAuthEntry, 0, 1)
	for _, entry := range entries {
		if strings.TrimSpace(entry.AuthIndex) != authIndex || strings.TrimSpace(entry.ID) == "" {
			continue
		}
		matches = append(matches, entry)
	}
	if len(matches) == 0 {
		return ""
	}
	if len(matches) == 1 {
		return strings.TrimSpace(matches[0].ID)
	}

	// AuthIndex should normally be unique. If a host returns duplicates, use
	// provider/type only as a disambiguator and fail closed unless that leaves
	// exactly one unique AuthID.
	matchedID := ""
	for _, entry := range matches {
		entryProvider := strings.TrimSpace(entry.Provider)
		if entryProvider == "" {
			entryProvider = strings.TrimSpace(entry.Type)
		}
		if provider == "" || !strings.EqualFold(entryProvider, provider) {
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
		idx := stringAny(m, "AuthIndex")
		if idx == "" {
			idx = stringAny(m, "auth_index")
		}
		idMatch := authID != "" && strings.TrimSpace(id) == authID
		indexMatch := authIndex != "" && strings.TrimSpace(idx) == authIndex
		if !idMatch && !indexMatch {
			continue
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
