package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

func handleAPIV4(req managementRequest) (map[string]any, error) {
	action := strings.TrimSpace(req.Query.Get("action"))
	if action == "" {
		action = "snapshot"
	}
	switch action {
	case "snapshot":
		return buildSnapshotV4(), nil
	case "events":
		return queryEventsV4(req.Query), nil
	case "save_policy":
		payload := payloadV4(req)
		if len(payload) == 0 {
			return nil, errors.New("payload is required")
		}
		var p Policy
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, fmt.Errorf("invalid policy payload: %w", err)
		}
		normalizePolicyV4(&p)
		resources := resourcesWithAliasesV82()
		tmp := V4State{Policies: map[string]*Policy{p.KeyFingerprint: &p}}
		rebindPoliciesV4(&tmp, resources)
		if rebound := tmp.Policies[p.KeyFingerprint]; rebound != nil {
			p = *rebound
		}
		if err := validatePolicy(&p); err != nil {
			return nil, err
		}
		v4Runtime.Lock()
		if v4Runtime.state.Policies == nil {
			v4Runtime.state.Policies = map[string]*Policy{}
		}
		replacement := clonePolicyV4(&p)
		resetChangedCandidateHealthLockedV4(v4Runtime.state.Policies[p.KeyFingerprint], replacement)
		v4Runtime.state.Policies[p.KeyFingerprint] = replacement
		pruneCandidateHealthLockedV4()
		v4Runtime.Unlock()
		if err := saveV4State(); err != nil {
			return nil, err
		}
		return buildSnapshotV4(), nil
	case "delete_policy":
		fp := strings.TrimSpace(req.Query.Get("fingerprint"))
		v4Runtime.Lock()
		delete(v4Runtime.state.Policies, fp)
		pruneCandidateHealthLockedV4()
		v4Runtime.Unlock()
		if err := saveV4State(); err != nil {
			return nil, err
		}
		return buildSnapshotV4(), nil
	case "save_resource_alias":
		payload := payloadV4(req)
		if len(payload) == 0 {
			return nil, errors.New("payload is required")
		}
		var in struct {
			ResourceID string `json:"resource_id"`
			Alias      string `json:"alias"`
		}
		if err := json.Unmarshal(payload, &in); err != nil {
			return nil, fmt.Errorf("invalid resource alias payload: %w", err)
		}
		in.ResourceID = strings.TrimSpace(in.ResourceID)
		in.Alias = strings.TrimSpace(in.Alias)
		if in.ResourceID == "" {
			return nil, errors.New("resource_id is required")
		}
		if len([]rune(in.Alias)) > 120 {
			return nil, errors.New("资源别名最多 120 个字符")
		}
		resources := resourcesWithExactIDsV10()
		var target *apiResource
		for i := range resources {
			if strings.TrimSpace(resources[i].ID) == in.ResourceID {
				target = &resources[i]
				break
			}
		}
		if target == nil {
			return nil, errors.New("当前资源不存在，无法保存别名")
		}
		aliasKey := resourceAliasKeyV82(*target)
		v4Runtime.Lock()
		if v4Runtime.state.ResourceAliases == nil {
			v4Runtime.state.ResourceAliases = map[string]string{}
		}
		if in.Alias == "" {
			delete(v4Runtime.state.ResourceAliases, aliasKey)
		} else {
			v4Runtime.state.ResourceAliases[aliasKey] = in.Alias
		}
		for _, p := range v4Runtime.state.Policies {
			if p == nil {
				continue
			}
			for _, rule := range p.Rules {
				if rule == nil {
					continue
				}
				for _, cand := range rule.Candidates {
					if cand == nil {
						continue
					}
					matched := strings.TrimSpace(cand.ResourceID) == in.ResourceID ||
						(strings.TrimSpace(cand.AuthIndex) != "" && strings.TrimSpace(cand.AuthIndex) == strings.TrimSpace(target.AuthIndex) &&
							strings.EqualFold(strings.TrimSpace(cand.Provider), strings.TrimSpace(target.Provider)))
					if matched {
						if in.Alias != "" {
							cand.Name = in.Alias
						} else if strings.TrimSpace(target.DisplayName) != "" {
							cand.Name = target.DisplayName
						}
					}
				}
			}
		}
		v4Runtime.Unlock()
		if err := saveV4State(); err != nil {
			return nil, err
		}
		return buildSnapshotV4(), nil
	case "diagnose":
		return diagnosePolicyV4(strings.TrimSpace(req.Query.Get("fingerprint")), strings.TrimSpace(req.Query.Get("model"))), nil
	case "save_observability":
		payload := payloadV4(req)
		if len(payload) == 0 {
			return nil, errors.New("payload is required")
		}
		var o ObservabilityConfig
		if err := json.Unmarshal(payload, &o); err != nil {
			return nil, err
		}
		o = normalizeObservability(o)
		v4Runtime.Lock()
		v4Runtime.state.Observability = o
		v4Runtime.Unlock()
		if err := saveV4State(); err != nil {
			return nil, err
		}
		if err := restartSQLiteSinkV4(); err != nil {
			return nil, err
		}
		return buildSnapshotV4(), nil
	case "clear_memory":
		v4Runtime.Lock()
		v4Runtime.recent = nil
		v4Runtime.Unlock()
		return buildSnapshotV4(), nil
	default:
		return nil, fmt.Errorf("unknown action %q", action)
	}
}

func payloadV4(req managementRequest) []byte {
	if len(req.Body) > 0 {
		return append([]byte(nil), req.Body...)
	}
	if x := req.Query.Get("payload"); x != "" {
		return []byte(x)
	}
	return nil
}

func normalizePolicyV4(p *Policy) {
	if p == nil {
		return
	}
	p.KeyFingerprint = strings.TrimSpace(p.KeyFingerprint)
	if p.Name == "" {
		p.Name = "策略 " + p.KeyHint
	}
	normalizeClientAffinityV1(p)
	for _, r := range p.Rules {
		normalizeRule(r)
	}
}

func buildSnapshotV4() map[string]any {
	keys, resources, configErr := currentEnvironment()
	v4Runtime.RLock()
	st := cloneV4State(v4Runtime.state)
	recent := append([]RoutingEvent(nil), v4Runtime.recent...)
	v4Runtime.RUnlock()
	if exact := resourcesWithExactIDsV10(); len(exact) > 0 {
		resources = exact
	}
	resources = applyResourceAliasesV82(resources, st.ResourceAliases)
	rebindPoliciesV4(&st, resources)
	keys = applyDownstreamAliasesV82(keys, st.Policies)
	policies := make([]*Policy, 0, len(st.Policies))
	for _, p := range st.Policies {
		policies = append(policies, p)
	}
	sort.Slice(policies, func(i, j int) bool { return policies[i].Name < policies[j].Name })

	recent = visibleRecentEventsV6(recent)
	if len(recent) > 200 {
		recent = recent[len(recent)-200:]
	}
	for i, j := 0, len(recent)-1; i < j; i, j = i+1, j-1 {
		recent[i], recent[j] = recent[j], recent[i]
	}

	runtimeState.RLock()
	schema := runtimeState.schema
	cfgPath := runtimeState.configPath
	runtimeState.RUnlock()
	return map[string]any{
		"ok":               true,
		"version":          pluginVersion,
		"schema":           schema,
		"config_path":      cfgPath,
		"config_error":     configErr,
		"downstream_keys":  keys,
		"resources":        resources,
		"policies":         policies,
		"observability":    st.Observability,
		"sqlite_status":    sqliteStatusV62(),
		"health_defaults":  candidateHealthDefaultsV4(),
		"candidate_health": candidateHealthSnapshotV4(),
		"recent_events":    recent,
	}
}

func visibleRecentEventsV6(events []RoutingEvent) []RoutingEvent {
	out := make([]RoutingEvent, 0, len(events))
	for _, ev := range events {
		if ev.Reason == "no_policy" {
			continue
		}
		out = append(out, ev)
	}
	return out
}
