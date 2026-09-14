package main

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

func diagnosePolicyV4(fp, model string) map[string]any {
	v4Runtime.RLock()
	p := clonePolicyV4(v4Runtime.state.Policies[fp])
	v4Runtime.RUnlock()
	if p == nil {
		return map[string]any{"ok": false, "error": "policy not found"}
	}
	resources := resourcesWithExactIDsV10()
	st := V4State{Policies: map[string]*Policy{fp: p}}
	rebindPoliciesV4(&st, resources)
	p = st.Policies[fp]
	_, rule := findPolicyRuleV4(fp, model)
	if rule == nil {
		return map[string]any{"ok": true, "policy": p, "model": model, "matched": false, "decision": decisionBypass, "note": "该模型没有命中任何 Rule，将由 CPA 默认路由处理"}
	}
	ranked := rankCandidatesV4(p, rule, http.Header{}, map[string]any{})
	items := []map[string]any{}
	now := time.Now()
	effectiveFound := false
	knownPriorities := map[int]struct{}{}
	unscopedCandidate := false
	for i, c := range ranked {
		selectable := candidateWouldBeSelectableV4(p, rule, c, now)
		effective := selectable && !effectiveFound
		if effective {
			effectiveFound = true
		}
		execModel, prefix := candidateScopedModelV10(c, model)
		liveID, identityErr := liveIDForCandidateV10(c)
		cpa := candidateCPADiagnosticV10(c)
		item := map[string]any{
			"order": i + 1, "name": c.Name, "provider": c.Provider,
			"auth_id": liveID, "auth_index": c.AuthIndex,
			"priority": c.Priority, "weight": c.Weight,
			"override_model": c.OverrideModel, "execution_model": execModel,
			"credential_prefix": prefix,
			"health": candidateHealthViewV4(p, rule, c),
			"selectable": selectable, "effective": effective,
			"cpa_priority_known": cpa.PriorityKnown,
			"cpa_runtime_known": cpa.RuntimeKnown,
			"cpa_status": cpa.Status,
			"cpa_unavailable": cpa.Unavailable,
			"cpa_diagnostic_source": cpa.Source,
		}
		if cpa.PriorityKnown {
			item["cpa_priority"] = cpa.Priority
			knownPriorities[cpa.Priority] = struct{}{}
		}
		if identityErr != nil {
			item["identity_error"] = identityErr.Error()
		}
		if prefix == "" {
			unscopedCandidate = true
			item["scope_note"] = "CPA host ABI has no forced-provider/auth field; exact scheduler pin is used, but a CPA-prefiltered credential may require a unique credential prefix"
		}
		items = append(items, item)
	}
	decision := decisionHandled
	if rule.Strategy == strategyCPADefault {
		decision = decisionBypass
	}
	resp := map[string]any{
		"ok": true, "policy": p, "model": model, "matched": true,
		"rule": rule, "strategy": rule.Strategy, "ranked_candidates": items,
		"decision": decision, "curl": buildDiagnosticCurlV4(p, model),
		"priority_note": "KCR Priority 仅决定 Rule 内顺序；CPA credential priority/cooldown 会在 scheduler 之前参与 eligibility 过滤。",
	}
	if len(knownPriorities) > 1 {
		values := make([]int, 0, len(knownPriorities))
		for priority := range knownPriorities {
			values = append(values, priority)
		}
		sort.Sort(sort.Reverse(sort.IntSlice(values)))
		resp["cpa_priority_warning"] = fmt.Sprintf("同一 Rule 检测到不同 CPA credential priority %v；无 prefix 候选可能在 KCR scheduler 前被 CPA 过滤。", values)
	}
	if unscopedCandidate {
		resp["scope_warning"] = "至少一个候选没有可证明的 prefix model scope；若其 exact Auth.ID 不在 CPA Candidates 中，KCR 会 fail closed 而不会改走其他 credential。"
	}
	return resp
}

func buildDiagnosticCurlV4(p *Policy, model string) string {
	if model == "" {
		model = "<MODEL>"
	}
	authHeader := "Author" + "ization: " + "Bear" + "er <策略 %s 对应的完整 CPA API Key>"
	return fmt.Sprintf("curl -i http://<CPA_HOST>:8317/v1/responses \\\n  -H '"+authHeader+"' \\\n  -H 'Content-Type: application/json' \\\n  -d '{\"model\":\"%s\",\"input\":\"只回复 ROUTE_OK\"}'", strings.ReplaceAll(p.Name, "'", ""), strings.ReplaceAll(model, "'", ""))
}
