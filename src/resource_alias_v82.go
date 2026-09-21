package main

import "strings"

func resourceAliasKeyV82(r apiResource) string {
	kind := strings.ToLower(strings.TrimSpace(r.Kind))
	provider := strings.ToLower(strings.TrimSpace(r.Provider))
	if idx := strings.TrimSpace(r.AuthIndex); idx != "" {
		return kind + "|" + provider + "|auth-index:" + idx
	}
	if id := strings.TrimSpace(r.AuthID); id != "" {
		return kind + "|" + provider + "|auth-id:" + id
	}
	return kind + "|" + provider + "|resource-id:" + strings.TrimSpace(r.ID)
}

func cloneResourceAliasesV82(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		if k = strings.TrimSpace(k); k != "" {
			if v = strings.TrimSpace(v); v != "" {
				out[k] = v
			}
		}
	}
	return out
}

func applyResourceAliasesV82(resources []apiResource, aliases map[string]string) []apiResource {
	out := append([]apiResource(nil), resources...)
	for i := range out {
		if alias := strings.TrimSpace(aliases[resourceAliasKeyV82(out[i])]); alias != "" {
			out[i].Alias = alias
		}
	}
	return out
}

func resourcesWithAliasesV82() []apiResource {
	resources := resourcesWithExactIDsV10()
	v4Runtime.RLock()
	aliases := cloneResourceAliasesV82(v4Runtime.state.ResourceAliases)
	v4Runtime.RUnlock()
	return applyResourceAliasesV82(resources, aliases)
}

func applyDownstreamAliasesV82(keys []downstreamKey, policies map[string]*Policy) []downstreamKey {
	out := append([]downstreamKey(nil), keys...)
	for i := range out {
		if p := policies[out[i].Fingerprint]; p != nil {
			out[i].Alias = strings.TrimSpace(p.Name)
		}
	}
	return out
}

func resourceAliasForCandidateV82(c *PolicyCandidate, resources []apiResource) string {
	if c == nil {
		return ""
	}
	for _, r := range resources {
		if strings.TrimSpace(c.ResourceID) != "" && strings.TrimSpace(r.ID) == strings.TrimSpace(c.ResourceID) {
			if alias := strings.TrimSpace(r.Alias); alias != "" {
				return alias
			}
			return strings.TrimSpace(r.DisplayName)
		}
		if strings.TrimSpace(c.AuthIndex) != "" && strings.TrimSpace(r.AuthIndex) == strings.TrimSpace(c.AuthIndex) &&
			strings.EqualFold(strings.TrimSpace(c.Provider), strings.TrimSpace(r.Provider)) {
			if alias := strings.TrimSpace(r.Alias); alias != "" {
				return alias
			}
			return strings.TrimSpace(r.DisplayName)
		}
	}
	return ""
}
