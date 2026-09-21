package main

import "strings"

func rebindPoliciesV4(st *V4State, resources []apiResource) {
	if st == nil {
		return
	}
	for _, p := range st.Policies {
		if p == nil {
			continue
		}
		for _, r := range p.Rules {
			if r == nil {
				continue
			}
			for _, c := range r.Candidates {
				if c == nil {
					continue
				}
				x, ok := exactRebindResourceV10(c, resources)
				if !ok {
					continue
				}
				c.ResourceID = x.ID
				c.ResourceKind = x.Kind
				c.Provider = x.Provider
				if strings.TrimSpace(c.AuthID) == "" && strings.TrimSpace(x.AuthID) != "" {
					c.AuthID = x.AuthID
				}
				if strings.TrimSpace(x.Alias) != "" {
					c.Name = x.Alias
				} else if c.Name == "" {
					c.Name = x.DisplayName
				}
			}
		}
	}
}
