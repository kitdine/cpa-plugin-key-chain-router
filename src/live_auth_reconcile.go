package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
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
// authoritative anymore. Exact current CPA runtime IDs are preferred; base URL
// is only a compatibility fallback and must still identify exactly one live auth.
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
	configRaw := string(data)
	_, configured := parseCPAConfig(configRaw)
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

	// Rebuild current CPA StableIDs from the legacy config entry, including
	// proxy-url, normalized prefix and deterministically sorted custom headers.
	exactIDs := legacyCurrentRuntimeIDsV8(configRaw, staleAuthIndex, provider)
	if len(exactIDs) > 0 {
		exactSet := make(map[string]struct{}, len(exactIDs))
		for _, id := range exactIDs {
			exactSet[id] = struct{}{}
		}
		exactMatches := make([]liveAuthEntryV8, 0, 1)
		for _, entry := range live.Files {
			if !liveAPIEntryMatchesProviderV8(entry, provider) {
				continue
			}
			if _, ok := exactSet[strings.TrimSpace(entry.ID)]; ok {
				exactMatches = append(exactMatches, entry)
			}
		}
		if len(exactMatches) == 1 {
			return strings.TrimSpace(exactMatches[0].ID), strings.TrimSpace(exactMatches[0].AuthIndex), nil
		}
		if len(exactMatches) > 1 {
			return "", "", fmt.Errorf("legacy API auth index %q maps to multiple exact live CPA credentials", staleAuthIndex)
		}
	}

	liveMatches := make([]liveAuthEntryV8, 0, 1)
	for _, entry := range live.Files {
		if !liveAPIEntryMatchesProviderV8(entry, provider) {
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

func liveAPIEntryMatchesProviderV8(entry liveAuthEntryV8, provider string) bool {
	entryProvider := strings.TrimSpace(entry.Provider)
	if entryProvider == "" {
		entryProvider = strings.TrimSpace(entry.Type)
	}
	return entry.RuntimeOnly && strings.TrimSpace(entry.ID) != "" && strings.TrimSpace(entry.AuthIndex) != "" && strings.EqualFold(entryProvider, strings.TrimSpace(provider))
}

func legacyCurrentRuntimeIDsV8(rawConfig, staleAuthIndex, provider string) []string {
	lines := preprocessYAMLLines(rawConfig)
	out := []string{}
	type simpleSpec struct {
		section, provider, oldPrefix, liveKind string
		vertex                                 bool
	}
	specs := []simpleSpec{
		{"codex-api-key", "codex", "codex-api-key", "codex:apikey", false},
		{"xai-api-key", "xai", "xai-api-key", "xai:apikey", false},
		{"claude-api-key", "claude", "claude-api-key", "claude:apikey", false},
		{"gemini-api-key", "gemini", "gemini-api-key", "gemini:apikey", false},
		{"interactions-api-key", "gemini-interactions", "interactions-api-key", "gemini-interactions:apikey", false},
		{"vertex-api-key", "vertex", "vertex", "vertex:apikey", true},
	}
	for _, spec := range specs {
		if !strings.EqualFold(spec.provider, provider) {
			continue
		}
		entries := parseTopMapList(lines, spec.section)
		headers := topMapHeadersV8(lines, spec.section)
		for i, e := range entries {
			key := scalar(e.Fields["api-key"])
			base := scalar(e.Fields["base-url"])
			proxyURL := scalar(e.Fields["proxy-url"])
			prefix := normalizePrefixV8(scalar(e.Fields["prefix"]))
			old := ""
			if spec.vertex {
				oldID := stableID("vertex:apikey", key, base, proxyURL)
				old = stableAuthIndex("id:" + oldID)
			} else {
				old = stableAuthIndex(spec.oldPrefix + ":" + base + "+" + key)
			}
			if old != staleAuthIndex {
				continue
			}
			if spec.vertex {
				out = append(out, stableID(spec.liveKind, key, base, proxyURL))
			} else {
				headerString := ""
				if i < len(headers) {
					headerString = formatSortedHeadersV8(headers[i])
				}
				out = append(out, stableID(spec.liveKind, key, base, proxyURL, prefix, headerString))
			}
		}
	}

	for _, e := range parseTopMapList(lines, "openai-compatibility") {
		if parseBool(scalar(e.Fields["disabled"]), false) {
			continue
		}
		name := scalar(e.Fields["name"])
		providerKey := openAICompatibleProviderKey(name)
		if !strings.EqualFold(providerKey, provider) {
			continue
		}
		providerName := strings.ToLower(strings.TrimSpace(name))
		if providerName == "" {
			providerName = "openai-compatibility"
		}
		liveKind := "openai-compatibility:" + providerName
		base := scalar(e.Fields["base-url"])
		entries := e.Nested["api-key-entries"]
		if len(entries) == 0 {
			entries = []yamlMapEntry{{Fields: map[string]string{}}}
		}
		for _, kent := range entries {
			key := scalar(kent.Fields["api-key"])
			proxyURL := scalar(kent.Fields["proxy-url"])
			old := ""
			if key != "" {
				old = stableAuthIndex("openai-compatibility:" + base + "+" + key)
			} else {
				oldID := stableID(liveKind, base)
				old = stableAuthIndex("id:" + oldID)
			}
			if old != staleAuthIndex {
				continue
			}
			if key == "" {
				out = append(out, stableID(liveKind, base))
			} else {
				out = append(out, stableID(liveKind, key, base, proxyURL))
			}
		}
	}
	return out
}

// topMapHeadersV8 extracts a simple `headers:` string map for each top-level
// list item in a config section. It intentionally mirrors only the shape used by
// CPA API-key credentials and leaves the general lightweight parser unchanged.
func topMapHeadersV8(lines []yamlLine, section string) []map[string]string {
	start, end := sectionBounds(lines, section)
	if start < 0 {
		return nil
	}
	out := []map[string]string{}
	for i := start; i < end; {
		if lines[i].Indent != 2 || !strings.HasPrefix(lines[i].Text, "-") {
			i++
			continue
		}
		itemEnd := i + 1
		for itemEnd < end && !(lines[itemEnd].Indent == 2 && strings.HasPrefix(lines[itemEnd].Text, "-")) {
			itemEnd++
		}
		headers := map[string]string{}
		for j := i + 1; j < itemEnd; j++ {
			if lines[j].Indent != 4 {
				continue
			}
			key, value, ok := splitYAMLKeyValue(lines[j].Text)
			if !ok || !strings.EqualFold(strings.TrimSpace(key), "headers") || strings.TrimSpace(value) != "" {
				continue
			}
			for k := j + 1; k < itemEnd && lines[k].Indent > 4; k++ {
				if lines[k].Indent != 6 {
					continue
				}
				hk, hv, ok := splitYAMLKeyValue(lines[k].Text)
				if !ok {
					continue
				}
				hk = strings.TrimSpace(scalar(hk))
				hv = strings.TrimSpace(scalar(hv))
				if hk != "" && hv != "" {
					headers[hk] = hv
				}
			}
			break
		}
		out = append(out, headers)
		i = itemEnd
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

func sameBaseURLV8(a, b string) bool {
	normalize := func(v string) string {
		v = strings.TrimSpace(v)
		v = strings.TrimRight(v, "/")
		return strings.ToLower(v)
	}
	return normalize(a) == normalize(b)
}
