package main

import (
	"encoding/json"
	"os"
	"strconv"
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
			Status: "configured; runtime cooldown/status not exposed by host.auth.list",
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
	lines := preprocessYAMLLines(string(raw))
	provider := strings.TrimSpace(c.Provider)
	idx := strings.TrimSpace(c.AuthIndex)
	if provider == "" || idx == "" {
		return 0, false
	}

	specs := []struct {
		section, provider, seed string
		vertex                  bool
	}{
		{"codex-api-key", "codex", "codex-api-key", false},
		{"xai-api-key", "xai", "xai-api-key", false},
		{"claude-api-key", "claude", "claude-api-key", false},
		{"gemini-api-key", "gemini", "gemini-api-key", false},
		{"interactions-api-key", "gemini-interactions", "interactions-api-key", false},
		{"vertex-api-key", "vertex", "vertex", true},
	}
	for _, spec := range specs {
		if !strings.EqualFold(provider, spec.provider) {
			continue
		}
		for _, e := range parseTopMapList(lines, spec.section) {
			key := scalar(e.Fields["api-key"])
			base := scalar(e.Fields["base-url"])
			proxyURL := scalar(e.Fields["proxy-url"])
			entryIndex := ""
			if spec.vertex {
				entryIndex = stableAuthIndex("id:" + stableID("vertex:apikey", key, base, proxyURL))
			} else {
				entryIndex = stableAuthIndex(spec.seed + ":" + base + "+" + key)
			}
			if entryIndex == idx {
				return yamlPriorityV10(e.Fields["priority"]), true
			}
		}
	}

	for _, e := range parseTopMapList(lines, "openai-compatibility") {
		if parseBool(scalar(e.Fields["disabled"]), false) {
			continue
		}
		if !strings.EqualFold(openAICompatibleProviderKey(scalar(e.Fields["name"])), provider) {
			continue
		}
		base := scalar(e.Fields["base-url"])
		entries := e.Nested["api-key-entries"]
		if len(entries) == 0 {
			entries = []yamlMapEntry{{Fields: map[string]string{}}}
		}
		for _, kent := range entries {
			key := scalar(kent.Fields["api-key"])
			entryIndex := ""
			if key != "" {
				entryIndex = stableAuthIndex("openai-compatibility:" + base + "+" + key)
			} else {
				name := strings.ToLower(strings.TrimSpace(scalar(e.Fields["name"])))
				if name == "" {
					name = "openai-compatibility"
				}
				entryIndex = stableAuthIndex("id:" + stableID("openai-compatibility:"+name, base))
			}
			if entryIndex == idx {
				return yamlPriorityV10(e.Fields["priority"]), true
			}
		}
	}
	return 0, false
}

func yamlPriorityV10(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(scalar(raw)))
	if err != nil {
		return 0
	}
	return n
}
