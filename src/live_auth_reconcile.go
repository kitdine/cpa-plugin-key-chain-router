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
// list item in a config section. It accepts block-style maps and both single-
// and multi-line flow-style maps accepted by CPA, while leaving the general
// lightweight parser unchanged.
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
			if !ok || !strings.EqualFold(strings.TrimSpace(key), "headers") {
				continue
			}
			if strings.TrimSpace(value) != "" {
				flow := collectFlowStringMapV8(value, lines, j+1, itemEnd)
				for hk, hv := range parseFlowStringMapV8(flow) {
					headers[hk] = hv
				}
				break
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

// collectFlowStringMapV8 completes a flow mapping that begins on the `headers:`
// line and may continue across subsequent YAML lines. It only joins while the
// outer braces remain unbalanced outside quotes; malformed collections remain
// malformed and parseFlowStringMapV8 will fail closed.
func collectFlowStringMapV8(initial string, lines []yamlLine, start, end int) string {
	flow := strings.TrimSpace(initial)
	if !strings.HasPrefix(flow, "{") || flowMapDepthV8(flow) <= 0 {
		return flow
	}
	for i := start; i < end && flowMapDepthV8(flow) > 0; i++ {
		flow += " " + strings.TrimSpace(lines[i].Text)
	}
	return strings.TrimSpace(flow)
}

func flowMapDepthV8(raw string) int {
	depth := 0
	var quote rune
	escaped := false
	for _, r := range raw {
		if escaped {
			escaped = false
			continue
		}
		if quote == '"' && r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		switch r {
		case '{':
			depth++
		case '}':
			depth--
		}
	}
	return depth
}

// parseFlowStringMapV8 parses the small YAML flow-map subset used by CPA's
// credential `headers` field. Commas and colons inside quoted scalars are kept
// as data, so common header values such as `"a,b:c"` hash identically to their
// block-style representation. Malformed entries are ignored rather than guessed.
func parseFlowStringMapV8(raw string) map[string]string {
	raw = strings.TrimSpace(raw)
	out := map[string]string{}
	if len(raw) < 2 || raw[0] != '{' || raw[len(raw)-1] != '}' || flowMapDepthV8(raw) != 0 {
		return out
	}
	body := strings.TrimSpace(raw[1 : len(raw)-1])
	if body == "" {
		return out
	}
	for _, entry := range splitFlowYAMLTopLevelV8(body, ',') {
		parts := splitFlowYAMLTopLevelV8(entry, ':')
		if len(parts) < 2 {
			continue
		}
		key := strings.TrimSpace(scalar(parts[0]))
		value := strings.TrimSpace(scalar(strings.Join(parts[1:], ":")))
		if key != "" && value != "" {
			out[key] = value
		}
	}
	return out
}

func splitFlowYAMLTopLevelV8(raw string, delimiter rune) []string {
	parts := []string{}
	start := 0
	var quote rune
	escaped := false
	for i, r := range raw {
		if escaped {
			escaped = false
			continue
		}
		if quote == '"' && r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == delimiter {
			parts = append(parts, raw[start:i])
			start = i + len(string(r))
		}
	}
	parts = append(parts, raw[start:])
	return parts
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
