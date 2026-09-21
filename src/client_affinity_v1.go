package main

import "strings"

const (
	clientAffinityOff    = "off"
	clientAffinityStrict = "strict"
)

func normalizeClientAffinityV1(p *Policy) {
	if p == nil {
		return
	}
	p.ClientAffinity = strings.ToLower(strings.TrimSpace(p.ClientAffinity))
	if p.ClientAffinity == "" {
		p.ClientAffinity = clientAffinityOff
	}
	p.ClientType = strings.ToLower(strings.TrimSpace(p.ClientType))
	p.ClientProvider = strings.ToLower(strings.TrimSpace(p.ClientProvider))

	// v0.8.0 stored the client identity in client_provider only when strict.
	// Preserve that state while moving the editor model to an independent
	// client_type field.
	if p.ClientType == "" && p.ClientProvider != "" {
		p.ClientType = p.ClientProvider
	}
	if p.ClientAffinity == clientAffinityStrict {
		p.ClientProvider = p.ClientType
	} else if p.ClientAffinity == clientAffinityOff {
		// Client type remains useful policy metadata even when affinity is off.
		p.ClientProvider = ""
	}
}

func effectiveClientProviderV81(p *Policy) string {
	if p == nil {
		return ""
	}
	if v := strings.ToLower(strings.TrimSpace(p.ClientType)); v != "" {
		return v
	}
	return strings.ToLower(strings.TrimSpace(p.ClientProvider))
}

func candidateAllowedByClientAffinityV1(p *Policy, c *PolicyCandidate) bool {
	if p == nil || c == nil {
		return false
	}
	if p.ClientAffinity == clientAffinityOff {
		return true
	}
	if p.ClientAffinity != clientAffinityStrict {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(c.ResourceKind), "OAuth") {
		return true
	}
	provider := effectiveClientProviderV81(p)
	return provider != "" && strings.EqualFold(strings.TrimSpace(c.Provider), provider)
}

func filterCandidatesByClientAffinityV1(p *Policy, xs []*PolicyCandidate) []*PolicyCandidate {
	if p == nil {
		return nil
	}
	if p.ClientAffinity == clientAffinityOff {
		return xs
	}
	if p.ClientAffinity != clientAffinityStrict {
		return nil
	}
	out := make([]*PolicyCandidate, 0, len(xs))
	for _, c := range xs {
		if candidateAllowedByClientAffinityV1(p, c) {
			out = append(out, c)
		}
	}
	return out
}
