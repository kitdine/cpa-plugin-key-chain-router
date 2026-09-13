package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const methodHostAuthGetV9 = "host.auth.get"

type hostAuthGetResponseV9 struct {
	AuthIndex string          `json:"auth_index"`
	Name      string          `json:"name,omitempty"`
	Path      string          `json:"path,omitempty"`
	JSON      json.RawMessage `json:"json"`
}

type apiResourceSlotV9 struct {
	Provider string
	Outer    int
	Inner    int
}

// scopeCandidateModelsV9 prepares request-local candidate clones for exact CPA
// routing without mutating the persisted Policy. CPA applies an auth Prefix as
// an additional model alias (unless ForceModelPrefix removes the bare alias), so
// sending prefix/model makes provider+credential resolution happen before the
// scheduler's priority filtering. KCR can then pin the exact AuthID inside that
// already-isolated candidate set.
//
// Config API resources also carry their original config slot in ResourceID. If
// the secret rotated in-place, the old synthetic AuthIndex changes; in that
// case we rebind the request-local clone to the credential currently occupying
// the same provider+config slot. We never search another provider or another
// slot, so this is not a fuzzy key substitution.
func scopeCandidateModelsV9(candidates []*PolicyCandidate, clientModel string) []*PolicyCandidate {
	if len(candidates) == 0 {
		return candidates
	}

	configResources := currentConfigResourcesV9()
	for _, c := range candidates {
		if c == nil {
			continue
		}

		prefix := ""
		if resource := currentConfigResourceForCandidateV9(c, configResources); resource != nil {
			// Reconcile secret rotation only on the exact persisted config slot.
			if strings.TrimSpace(resource.AuthIndex) != "" {
				c.AuthIndex = strings.TrimSpace(resource.AuthIndex)
			}
			c.ResourceID = resource.ID
			prefix = strings.Trim(strings.TrimSpace(resource.Prefix), "/")
		} else if !strings.EqualFold(strings.TrimSpace(c.ResourceKind), "API") {
			prefix = authFilePrefixV9(c.AuthIndex)
		}

		if prefix == "" {
			continue
		}
		model := strings.TrimSpace(c.OverrideModel)
		if model == "" {
			model = strings.TrimSpace(clientModel)
		}
		if model == "" || strings.HasPrefix(model, prefix+"/") {
			continue
		}
		c.OverrideModel = prefix + "/" + model
	}
	return candidates
}

func currentConfigResourcesV9() []apiResource {
	runtimeState.RLock()
	path := strings.TrimSpace(runtimeState.configPath)
	runtimeState.RUnlock()
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return nil
	}
	_, resources := parseCPAConfig(string(raw))
	return resources
}

func currentConfigResourceForCandidateV9(c *PolicyCandidate, resources []apiResource) *apiResource {
	if c == nil || len(resources) == 0 {
		return nil
	}
	provider := strings.TrimSpace(c.Provider)
	authIndex := strings.TrimSpace(c.AuthIndex)

	// Prefer the exact current identity. This is the normal no-migration path.
	var exact *apiResource
	for i := range resources {
		r := &resources[i]
		if !strings.EqualFold(strings.TrimSpace(r.Kind), "API") || !strings.EqualFold(strings.TrimSpace(r.Provider), provider) {
			continue
		}
		if authIndex != "" && strings.TrimSpace(r.AuthIndex) == authIndex {
			if exact != nil {
				return nil
			}
			exact = r
		}
	}
	if exact != nil {
		return exact
	}

	// A stale synthetic AuthIndex can only migrate through the exact config slot
	// encoded by KCR's own ResourceID. Provider and both slot indexes must match.
	if !strings.EqualFold(strings.TrimSpace(c.ResourceKind), "API") {
		return nil
	}
	want, ok := parseAPIResourceSlotV9(c.ResourceID)
	if !ok || !strings.EqualFold(want.Provider, provider) {
		return nil
	}
	var matched *apiResource
	for i := range resources {
		r := &resources[i]
		got, okSlot := parseAPIResourceSlotV9(r.ID)
		if !okSlot || !strings.EqualFold(got.Provider, want.Provider) || got.Outer != want.Outer || got.Inner != want.Inner {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(r.Provider), provider) {
			continue
		}
		if matched != nil {
			return nil
		}
		matched = r
	}
	return matched
}

func parseAPIResourceSlotV9(resourceID string) (apiResourceSlotV9, bool) {
	parts := strings.Split(strings.TrimSpace(resourceID), ":")
	if len(parts) != 5 || parts[0] != "api" || strings.TrimSpace(parts[1]) == "" {
		return apiResourceSlotV9{}, false
	}
	outer, errOuter := strconv.Atoi(parts[3])
	inner, errInner := strconv.Atoi(parts[4])
	if errOuter != nil || errInner != nil || outer < 0 || inner < 0 {
		return apiResourceSlotV9{}, false
	}
	return apiResourceSlotV9{Provider: strings.TrimSpace(parts[1]), Outer: outer, Inner: inner}, true
}

func authFilePrefixV9(authIndex string) string {
	authIndex = strings.TrimSpace(authIndex)
	if authIndex == "" {
		return ""
	}
	raw, err := callHost(methodHostAuthGetV9, map[string]any{"auth_index": authIndex})
	if err != nil {
		return ""
	}
	var resp hostAuthGetResponseV9
	if err := json.Unmarshal(raw, &resp); err != nil || len(resp.JSON) == 0 {
		return ""
	}
	var metadata map[string]any
	if err := json.Unmarshal(resp.JSON, &metadata); err != nil {
		return ""
	}
	prefix, _ := metadata["prefix"].(string)
	return strings.Trim(strings.TrimSpace(prefix), "/")
}

func candidateScopeDebugV9(c *PolicyCandidate, clientModel string) string {
	if c == nil {
		return ""
	}
	model := strings.TrimSpace(c.OverrideModel)
	if model == "" {
		model = strings.TrimSpace(clientModel)
	}
	return fmt.Sprintf("provider=%s auth_index=%s model=%s", c.Provider, c.AuthIndex, model)
}
