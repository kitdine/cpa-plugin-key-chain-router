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
	_, resources, _ := currentEnvironment()
	var target *apiResource
	for i := range resources {
		r := &resources[i]
		if !strings.EqualFold(strings.TrimSpace(r.Provider), strings.TrimSpace(c.Provider)) {
			continue
		}
		if strings.TrimSpace(c.AuthIndex) != "" && strings.TrimSpace(r.AuthIndex) == strings.TrimSpace(c.AuthIndex) {
			if target != nil {
				return model, ""
			}
			target = r
		}
	}
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
