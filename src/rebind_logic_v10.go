package main

import "strings"

func exactRebindResourceV10(candidate *PolicyCandidate, resources []apiResource) (apiResource, bool) {
	if candidate == nil {
		return apiResource{}, false
	}
	provider := strings.TrimSpace(candidate.Provider)
	if provider == "" {
		return apiResource{}, false
	}

	if authID := strings.TrimSpace(candidate.AuthID); authID != "" {
		var matched *apiResource
		for i := range resources {
			r := &resources[i]
			if !strings.EqualFold(strings.TrimSpace(r.Provider), provider) || strings.TrimSpace(r.AuthID) != authID {
				continue
			}
			if matched != nil {
				return apiResource{}, false
			}
			matched = r
		}
		if matched == nil {
			return apiResource{}, false
		}
		return *matched, true
	}

	resourceID := strings.TrimSpace(candidate.ResourceID)
	if resourceID != "" {
		var matched *apiResource
		for i := range resources {
			r := &resources[i]
			if strings.TrimSpace(r.ID) != resourceID || !strings.EqualFold(strings.TrimSpace(r.Provider), provider) {
				continue
			}
			if matched != nil {
				return apiResource{}, false
			}
			matched = r
		}
		if matched != nil {
			return *matched, true
		}
	}

	idx := strings.TrimSpace(candidate.AuthIndex)
	if idx == "" {
		return apiResource{}, false
	}
	var matched *apiResource
	for i := range resources {
		r := &resources[i]
		if !strings.EqualFold(strings.TrimSpace(r.Provider), provider) || strings.TrimSpace(r.AuthIndex) != idx {
			continue
		}
		if matched != nil {
			return apiResource{}, false
		}
		matched = r
	}
	if matched == nil {
		return apiResource{}, false
	}
	return *matched, true
}
