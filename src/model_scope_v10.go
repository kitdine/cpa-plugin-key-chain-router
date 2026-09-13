package main

import "strings"

func candidateScopedModelV10(c *PolicyCandidate, clientModel string) (string, string) {
	model := strings.TrimSpace(clientModel)
	if c != nil && strings.TrimSpace(c.OverrideModel) != "" { model = strings.TrimSpace(c.OverrideModel) }
	if c == nil || model == "" { return model, "" }
	resources := resourcesForExecutionV10()
	target := exactResourceForCandidateV10(c, resources)
	if target == nil { return model, "" }
	prefix := strings.Trim(strings.TrimSpace(target.Prefix), "/")
	if prefix == "" { return model, "" }

	// For config API credentials, an empty parsed Models list is ambiguous: it
	// can mean no explicit models or a YAML form KCR's lightweight parser did not
	// understand. Never fabricate prefix/<model> in that case. OAuth/file auths
	// get their model set from CPA runtime registration and can safely use prefix.
	if strings.EqualFold(strings.TrimSpace(target.Kind), "API") && len(target.Models) == 0 {
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
			if strings.EqualFold(strings.TrimSpace(registered), base) { ok = true; break }
		}
		if !ok { return model, "" }
	}
	return prefix + "/" + base, prefix
}

func scopeCandidateModelsV9(candidates []*PolicyCandidate, _ string) []*PolicyCandidate { return candidates }
