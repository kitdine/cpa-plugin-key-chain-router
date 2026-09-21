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
	switch p.ClientAffinity {
	case clientAffinityStrict:
	default:
		p.ClientAffinity = clientAffinityOff
	}
	p.ClientProvider = strings.ToLower(strings.TrimSpace(p.ClientProvider))
	if p.ClientAffinity == clientAffinityOff {
		p.ClientProvider = ""
	}
}

func candidateAllowedByClientAffinityV1(p *Policy, c *PolicyCandidate) bool {
	if p == nil || c == nil {
		return false
	}
	if p.ClientAffinity != clientAffinityStrict {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(c.ResourceKind), "OAuth") {
		return true
	}
	return p.ClientProvider != "" && strings.EqualFold(strings.TrimSpace(c.Provider), p.ClientProvider)
}

func filterCandidatesByClientAffinityV1(p *Policy, xs []*PolicyCandidate) []*PolicyCandidate {
	if p == nil || p.ClientAffinity != clientAffinityStrict {
		return xs
	}
	out := make([]*PolicyCandidate, 0, len(xs))
	for _, c := range xs {
		if candidateAllowedByClientAffinityV1(p, c) {
			out = append(out, c)
		}
	}
	return out
}
