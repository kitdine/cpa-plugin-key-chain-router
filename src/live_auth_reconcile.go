package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type liveAuthListResponseV8 struct {
	Files []liveAuthEntryV8 `json:"files"`
}

type liveAuthEntryV8 struct {
	ID          string `json:"id,omitempty"`
	AuthIndex   string `json:"auth_index,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Type        string `json:"type,omitempty"`
	BaseURL     string `json:"base_url,omitempty"`
	RuntimeOnly bool   `json:"runtime_only,omitempty"`
}

// resolveLegacySyntheticAuthV8 bridges policies created by older KCR versions
// that synthesized API-provider AuthIndex values from config.yaml. Current CPA
// owns credential identity in its live AuthManager, so those hashes are not
// authoritative anymore. We only translate when both sides are unique.
func resolveLegacySyntheticAuthV8(rawAuthList []byte, staleAuthIndex, provider string) (string, string, error) {
	staleAuthIndex = strings.TrimSpace(staleAuthIndex)
	provider = strings.TrimSpace(provider)
	if staleAuthIndex == "" || provider == "" {
		return "", "", nil
	}

	runtimeState.RLock()
	configPath := runtimeState.configPath
	runtimeState.RUnlock()
	if strings.TrimSpace(configPath) == "" {
		return "", "", nil
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", "", fmt.Errorf("read CPA config for legacy auth reconciliation: %w", err)
	}
	_, configured := parseCPAConfig(string(data))
	configMatches := make([]apiResource, 0, 1)
	for _, resource := range configured {
		if !strings.EqualFold(strings.TrimSpace(resource.Provider), provider) {
			continue
		}
		if strings.TrimSpace(resource.AuthIndex) != staleAuthIndex {
			continue
		}
		configMatches = append(configMatches, resource)
	}
	if len(configMatches) == 0 {
		return "", "", nil
	}
	if len(configMatches) != 1 {
		return "", "", fmt.Errorf("legacy API auth index %q is ambiguous in CPA config", staleAuthIndex)
	}
	legacy := configMatches[0]

	var live liveAuthListResponseV8
	if err := json.Unmarshal(rawAuthList, &live); err != nil {
		return "", "", fmt.Errorf("decode live auth list for legacy reconciliation: %w", err)
	}
	liveMatches := make([]liveAuthEntryV8, 0, 1)
	for _, entry := range live.Files {
		entryProvider := strings.TrimSpace(entry.Provider)
		if entryProvider == "" {
			entryProvider = strings.TrimSpace(entry.Type)
		}
		if !strings.EqualFold(entryProvider, provider) || strings.TrimSpace(entry.ID) == "" || strings.TrimSpace(entry.AuthIndex) == "" {
			continue
		}
		// API-key credentials synthesized from config are runtime-only in CPA.
		// Requiring this prevents an OAuth file with the same provider/base URL
		// from being mistaken for the configured API provider.
		if !entry.RuntimeOnly {
			continue
		}
		if !sameBaseURLV8(entry.BaseURL, legacy.BaseURL) {
			continue
		}
		liveMatches = append(liveMatches, entry)
	}
	if len(liveMatches) == 0 {
		return "", "", nil
	}
	if len(liveMatches) != 1 {
		return "", "", fmt.Errorf("legacy API auth index %q matches %d live CPA credentials; refusing to guess", staleAuthIndex, len(liveMatches))
	}
	return strings.TrimSpace(liveMatches[0].ID), strings.TrimSpace(liveMatches[0].AuthIndex), nil
}

func sameBaseURLV8(a, b string) bool {
	normalize := func(v string) string {
		v = strings.TrimSpace(v)
		v = strings.TrimRight(v, "/")
		return strings.ToLower(v)
	}
	return normalize(a) == normalize(b)
}
