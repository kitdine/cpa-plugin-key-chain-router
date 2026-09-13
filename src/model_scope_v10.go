package main

import "strings"

func candidateScopedModelV10(c *PolicyCandidate, clientModel string) (string, string) {
	model := strings.TrimSpace(clientModel)
	if c != nil && strings.TrimSpace(c.OverrideModel) != "" {
		model = strings.TrimSpace(c.OverrideModel)
	}
	if c == nil || model == "" {
		return model, ""
	}
	resources := resourcesWithExactIDsV10()
	target := exactResourceForCandidateV10(c, resources)
	if target == nil {
		return model, ""
	}
	prefix := strings.Trim(strings.TrimSpace(target.Prefix), "/")
	if prefix == "" {
		return model, ""
	}
	base := model
	for _, r := range resources {
		p := strings.Trim(strings.TrimSpace(r.Prefix), "/")
		if p != "" && strings.HasPrefix(base, p+"/") {
			base = strings.TrimSpace(strings.TrimPrefix(base, p+"/"))
			break
		}
	}
	if len(target.Models) > 0 {
		ok := false
		for _, registered := range target.Models {
			if strings.EqualFold(strings.TrimSpace(registered), base) {
				ok = true
				break
			}
		}
		if !ok {
			return model, ""
		}
	}
	return prefix + "/" + base, prefix
}

// scopeCandidateModelsV9 is kept at the existing call site but now implements
// the v0.7 execution plan: request-local clones use an exact live Auth.ID token
// and, where CPA already registered one, a credential-qualified model prefix.
// Persisted policy identity is retained in hidden originals for health checks.
func scopeCandidateModelsV9(candidates []*PolicyCandidate, clientModel string) []*PolicyCandidate {
	for _, c := range candidates {
		if c == nil {
			continue
		}
		originalOverride := c.OverrideModel
		originalAuthIndex := c.AuthIndex
		scopedModel, _ := candidateScopedModelV10(c, clientModel)
		currentModel := strings.TrimSpace(c.OverrideModel)
		if currentModel == "" {
			currentModel = strings.TrimSpace(clientModel)
		}
		liveID, _ := liveIDForCandidateV10(c)
		modelChanged := scopedModel != "" && scopedModel != currentModel
		authChanged := strings.TrimSpace(liveID) != ""
		if !modelChanged && !authChanged {
			continue
		}
		c.executionScoped = true
		c.executionOriginalOverride = originalOverride
		c.executionOriginalAuthIndex = originalAuthIndex
		if modelChanged {
			c.OverrideModel = scopedModel
		}
		if authChanged {
			c.AuthIndex = directLiveIdentityPrefixV10 + strings.TrimSpace(liveID)
		}
	}
	return candidates
}
