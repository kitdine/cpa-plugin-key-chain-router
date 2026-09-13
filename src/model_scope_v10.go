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

// The scheduler resolves the candidate's persisted AuthIndex/AuthID to the
// authoritative live CPA Auth.ID on every ticket claim. Only the request model
// is rewritten here; auth identity remains unchanged so health/config checks
// retain their persisted identity without any special-case auth comparison.
func scopeCandidateModelsV9(candidates []*PolicyCandidate, clientModel string) []*PolicyCandidate {
	for _, c := range candidates {
		if c == nil {
			continue
		}
		scopedModel, _ := candidateScopedModelV10(c, clientModel)
		currentModel := strings.TrimSpace(c.OverrideModel)
		if currentModel == "" {
			currentModel = strings.TrimSpace(clientModel)
		}
		if scopedModel == "" || scopedModel == currentModel {
			continue
		}
		c.executionScoped = true
		c.executionOriginalOverride = c.OverrideModel
		c.OverrideModel = scopedModel
	}
	return candidates
}
