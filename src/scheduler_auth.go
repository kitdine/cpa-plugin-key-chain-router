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
	if id == "" {
		return "", fmt.Errorf("auth index %q not found in host.auth.list", authIndex)
	}
	return id, nil
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
	// provider/type only as a disambiguator instead of allowing an arbitrary pick.
	for _, entry := range matches {
		entryProvider := strings.TrimSpace(entry.Provider)
		if entryProvider == "" {
			entryProvider = strings.TrimSpace(entry.Type)
		}
		if provider != "" && strings.EqualFold(entryProvider, provider) {
			return strings.TrimSpace(entry.ID)
		}
	}
	return ""
}
