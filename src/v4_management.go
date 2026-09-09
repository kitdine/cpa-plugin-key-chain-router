package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

func handleAPIV4(req managementRequest) (map[string]any, error) {
	action := strings.TrimSpace(req.Query.Get("action")); if action == "" { action = "snapshot" }
	switch action {
	case "snapshot": return buildSnapshotV4(), nil
	case "save_policy":
		payload := req.Query.Get("payload"); if payload == "" { return nil, errors.New("payload is required") }
		var p Policy; if err := json.Unmarshal([]byte(payload), &p); err != nil { return nil, fmt.Errorf("invalid policy payload: %w", err) }
		normalizePolicyV4(&p); if err := validatePolicy(&p); err != nil { return nil, err }
		v4Runtime.Lock(); if v4Runtime.state.Policies == nil { v4Runtime.state.Policies = map[string]*Policy{} }; v4Runtime.state.Policies[p.KeyFingerprint] = clonePolicyV4(&p); v4Runtime.Unlock()
		if err := saveV4State(); err != nil { return nil, err }; return buildSnapshotV4(), nil
	case "delete_policy":
		fp := strings.TrimSpace(req.Query.Get("fingerprint")); v4Runtime.Lock(); delete(v4Runtime.state.Policies, fp); v4Runtime.Unlock(); if err := saveV4State(); err != nil { return nil, err }; return buildSnapshotV4(), nil
	case "diagnose": return diagnosePolicyV4(strings.TrimSpace(req.Query.Get("fingerprint")), strings.TrimSpace(req.Query.Get("model"))), nil
	case "save_observability":
		payload := req.Query.Get("payload"); var o ObservabilityConfig; if err := json.Unmarshal([]byte(payload), &o); err != nil { return nil, err }; o = normalizeObservability(o)
		v4Runtime.Lock(); v4Runtime.state.Observability = o; v4Runtime.Unlock(); if err := saveV4State(); err != nil { return nil, err }; if err := restartSQLiteSinkV4(); err != nil { return nil, err }; return buildSnapshotV4(), nil
	case "clear_memory": v4Runtime.Lock(); v4Runtime.recent = nil; v4Runtime.Unlock(); return buildSnapshotV4(), nil
	default: return nil, fmt.Errorf("unknown action %q", action)
	}
}

func normalizePolicyV4(p *Policy) { if p == nil { return }; p.KeyFingerprint = strings.TrimSpace(p.KeyFingerprint); if p.Name == "" { p.Name = "策略 " + p.KeyHint }; for _, r := range p.Rules { normalizeRule(r) } }

func buildSnapshotV4() map[string]any {
	keys, resources, configErr := currentEnvironment(); v4Runtime.RLock(); st := cloneV4State(v4Runtime.state); recent := append([]RoutingEvent(nil), v4Runtime.recent...); v4Runtime.RUnlock(); rebindPoliciesV4(&st, resources)
	policies := make([]*Policy, 0, len(st.Policies)); for _, p := range st.Policies { policies = append(policies, p) }; sort.Slice(policies, func(i, j int) bool { return policies[i].Name < policies[j].Name })
	if len(recent) > 200 { recent = recent[len(recent)-200:] }; for i, j := 0, len(recent)-1; i < j; i, j = i+1, j-1 { recent[i], recent[j] = recent[j], recent[i] }
	runtimeState.RLock(); schema := runtimeState.schema; cfgPath := runtimeState.configPath; runtimeState.RUnlock()
	return map[string]any{"ok": true, "version": pluginVersion, "schema": schema, "config_path": cfgPath, "config_error": configErr, "downstream_keys": keys, "resources": resources, "policies": policies, "observability": st.Observability, "recent_events": recent}
}

func rebindPoliciesV4(st *V4State, resources []apiResource) {
	byID := map[string]apiResource{}; byIdx := map[string]apiResource{}; for _, x := range resources { byID[x.ID] = x; if x.AuthIndex != "" { byIdx[x.AuthIndex] = x } }
	for _, p := range st.Policies { for _, r := range p.Rules { for _, c := range r.Candidates { if c == nil { continue }; x, ok := byID[c.ResourceID]; if !ok && c.AuthIndex != "" { x, ok = byIdx[c.AuthIndex] }; if ok { c.ResourceID = x.ID; c.ResourceKind = x.Kind; c.Provider = x.Provider; if c.Name == "" { c.Name = x.DisplayName } } } } }
}

func diagnosePolicyV4(fp, model string) map[string]any {
	v4Runtime.RLock(); p := clonePolicyV4(v4Runtime.state.Policies[fp]); v4Runtime.RUnlock(); if p == nil { return map[string]any{"ok": false, "error": "policy not found"} }
	_, resources, _ := currentEnvironment(); st := V4State{Policies: map[string]*Policy{fp: p}}; rebindPoliciesV4(&st, resources); p = st.Policies[fp]
	_, rule := findPolicyRuleV4(fp, model); if rule == nil { return map[string]any{"ok": true, "policy": p, "model": model, "matched": false, "decision": decisionBypass, "note": "该模型没有命中任何 Rule，将由 CPA 默认路由处理"} }
	ranked := rankCandidatesV4(p, rule, http.Header{}, map[string]any{}); items := []map[string]any{}; for i, c := range ranked { items = append(items, map[string]any{"order": i + 1, "name": c.Name, "provider": c.Provider, "auth_index": c.AuthIndex, "priority": c.Priority, "weight": c.Weight, "override_model": c.OverrideModel}) }
	decision := decisionHandled; if rule.Strategy == strategyCPADefault { decision = decisionBypass }
	return map[string]any{"ok": true, "policy": p, "model": model, "matched": true, "rule": rule, "strategy": rule.Strategy, "ranked_candidates": items, "decision": decision, "curl": buildDiagnosticCurlV4(p, model)}
}

func buildDiagnosticCurlV4(p *Policy, model string) string {
	if model == "" { model = "<MODEL>" }
	return fmt.Sprintf("curl -i http://<CPA_HOST>:8317/v1/responses \\\n  -H 'Authorization: Bearer <策略 %s 对应的完整 CPA API Key>' \\\n  -H 'Content-Type: application/json' \\\n  -d '{\"model\":\"%s\",\"input\":\"只回复 ROUTE_OK\"}'", strings.ReplaceAll(p.Name, "'", ""), strings.ReplaceAll(model, "'", ""))
}
