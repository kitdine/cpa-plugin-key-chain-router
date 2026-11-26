package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// resolveLegacySyntheticAuthIDV8 bridges policies created by KCR <= v0.6.7
// that synthesized API-provider AuthIndex values from config.yaml. Current CPA
// config API-key credentials are in-memory AuthManager records and are not
// necessarily exposed by host.auth.list, so the bridge reconstructs the exact
// current CPA StableID. handleSchedulerPick then requires that exact AuthID to
// exist in the live SchedulerPickRequest.Candidates set.
func resolveLegacySyntheticAuthIDV8(staleAuthIndex, provider string) (string, error) {
	staleAuthIndex = strings.TrimSpace(staleAuthIndex)
	provider = strings.TrimSpace(provider)
	if staleAuthIndex == "" || provider == "" {
		return "", nil
	}

	runtimeState.RLock()
	configPath := runtimeState.configPath
	runtimeState.RUnlock()
	if strings.TrimSpace(configPath) == "" {
		return "", nil
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("read CPA config for legacy auth reconciliation: %w", err)
	}
	configRaw := string(data)
	_, configured := parseCPAConfig(configRaw)
	matches := 0
	for _, resource := range configured {
		if strings.EqualFold(strings.TrimSpace(resource.Provider), provider) && strings.TrimSpace(resource.AuthIndex) == staleAuthIndex {
			matches++
		}
	}
	if matches == 0 {
		return "", nil
	}
	if matches != 1 {
		return "", fmt.Errorf("legacy API auth index %q is ambiguous in CPA config", staleAuthIndex)
	}

	exactIDs := legacyCurrentRuntimeIDsV8(configRaw, staleAuthIndex, provider)
	if len(exactIDs) == 0 {
		return "", fmt.Errorf("legacy API auth index %q cannot be mapped to an exact current CPA runtime identity", staleAuthIndex)
	}
	if len(exactIDs) != 1 {
		return "", fmt.Errorf("legacy API auth index %q maps to multiple current CPA runtime identities", staleAuthIndex)
	}
	return strings.TrimSpace(exactIDs[0]), nil
}

func legacyCurrentRuntimeIDsV8(rawConfig, staleAuthIndex, provider string) []string {
	inv, err := readCPAConfigInventory(rawConfig)
	if err != nil {
		return nil
	}
	var out []string
	for _, item := range inv.resources {
		if strings.EqualFold(provider, item.resource.Provider) && staleAuthIndex == item.resource.AuthIndex {
			out = append(out, item.liveID)
		}
	}
	return out
}

func formatSortedHeadersV8(headers map[string]string) string {
	if len(headers) == 0 {
		return ""
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		b.WriteString(key)
		b.WriteByte(0)
		b.WriteString(headers[key])
		b.WriteByte(0)
	}
	return b.String()
}

func normalizePrefixV8(v string) string {
	v = strings.TrimSpace(v)
	v = strings.Trim(v, "/")
	if strings.Contains(v, "/") {
		return ""
	}
	return v
}
