package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func defaultObservability() ObservabilityConfig {
	return ObservabilityConfig{MemoryEnabled: true, MemoryLimit: 500, LogEnabled: true, LogLevel: "info", SQLiteEnabled: false, SQLitePath: "key-chain-router.db", SQLiteRetentionDays: 30, SQLiteMaxRows: 100000, ResponseHeaders: false}
}

func defaultFailover() FailoverPolicy {
	return FailoverPolicy{Network: failNext, Unauthorized: failNext, Timeout: failNext, Conflict: failNext, RateLimit: failNext, ServerError: failNext, Other: failStop, Exhausted: "error", MaxAttempts: 0}
}

func normalizeFailover(f FailoverPolicy) FailoverPolicy {
	d := defaultFailover()
	if !validFailAction(f.Network) {
		f.Network = d.Network
	}
	if !validFailAction(f.Unauthorized) {
		f.Unauthorized = d.Unauthorized
	}
	if !validFailAction(f.Timeout) {
		f.Timeout = d.Timeout
	}
	if !validFailAction(f.Conflict) {
		f.Conflict = d.Conflict
	}
	if !validFailAction(f.RateLimit) {
		f.RateLimit = d.RateLimit
	}
	if !validFailAction(f.ServerError) {
		f.ServerError = d.ServerError
	}
	if !validFailAction(f.Other) {
		f.Other = d.Other
	}
	if f.Exhausted != "error" && f.Exhausted != failCPADefault {
		f.Exhausted = d.Exhausted
	}
	if f.MaxAttempts < 0 {
		f.MaxAttempts = 0
	}
	return f
}

func validFailAction(s string) bool {
	switch strings.TrimSpace(s) {
	case failNext, failSamePriorityFirst, failNextPriority, failStop, failCPADefault:
		return true
	default:
		return false
	}
}

func normalizeObservability(o ObservabilityConfig) ObservabilityConfig {
	d := defaultObservability()
	if o.MemoryLimit <= 0 {
		o.MemoryLimit = d.MemoryLimit
	}
	if o.MemoryLimit > 10000 {
		o.MemoryLimit = 10000
	}
	if strings.TrimSpace(o.LogLevel) == "" {
		o.LogLevel = d.LogLevel
	}
	switch strings.ToLower(o.LogLevel) {
	case "debug", "info", "warn", "error":
		o.LogLevel = strings.ToLower(o.LogLevel)
	default:
		o.LogLevel = d.LogLevel
	}
	if strings.TrimSpace(o.SQLitePath) == "" {
		o.SQLitePath = d.SQLitePath
	}
	if o.SQLiteRetentionDays <= 0 {
		o.SQLiteRetentionDays = d.SQLiteRetentionDays
	}
	if o.SQLiteMaxRows <= 0 {
		o.SQLiteMaxRows = d.SQLiteMaxRows
	}
	if o.SQLiteMaxRows > 5000000 {
		o.SQLiteMaxRows = 5000000
	}
	return o
}

func configureV4(statePath string, legacy State) error {
	st := V4State{Version: v4StateVersion, UpdatedAt: time.Now().UTC().Format(time.RFC3339), Policies: map[string]*Policy{}, Observability: defaultObservability()}
	if raw, err := os.ReadFile(statePath); err == nil && len(strings.TrimSpace(string(raw))) > 0 {
		var probe struct {
			Version int `json:"version"`
		}
		_ = json.Unmarshal(raw, &probe)
		if probe.Version >= v4StateVersion {
			if err := json.Unmarshal(raw, &st); err != nil {
				return fmt.Errorf("decode v4 state: %w", err)
			}
		} else {
			st = migrateLegacyV4(legacy)
		}
	} else if len(legacy.Routes) > 0 {
		st = migrateLegacyV4(legacy)
	}
	if st.Policies == nil {
		st.Policies = map[string]*Policy{}
	}
	st.Observability = normalizeObservability(st.Observability)
	normalizePolicies(&st)
	v4Runtime.Lock()
	v4Runtime.state = st
	v4Runtime.recent = nil
	v4Runtime.rr = map[string]uint64{}
	v4Runtime.smooth = map[string]map[string]int{}
	v4Runtime.health = map[string]*candidateHealthState{}
	v4Runtime.Unlock()
	return restartSQLiteSinkV4()
}

func migrateLegacyV4(legacy State) V4State {
	st := V4State{Version: v4StateVersion, UpdatedAt: time.Now().UTC().Format(time.RFC3339), Policies: map[string]*Policy{}, Observability: defaultObservability()}
	routes := make([]*Route, 0, len(legacy.Routes))
	for _, r := range legacy.Routes {
		if r != nil {
			routes = append(routes, r)
		}
	}
	sort.SliceStable(routes, func(i, j int) bool { return routes[i].Name < routes[j].Name })
	for _, r := range routes {
		fp := strings.TrimSpace(r.KeyFingerprint)
		if fp == "" {
			continue
		}
		p := st.Policies[fp]
		if p == nil {
			p = &Policy{Name: r.Name, KeyFingerprint: fp, KeyHint: r.KeyHint, Enabled: r.Enabled, Rules: []*PolicyRule{}}
			if p.Name == "" {
				p.Name = "策略 " + r.KeyHint
			}
			st.Policies[fp] = p
		}
		rule := &PolicyRule{ID: "rule-" + randomShort(), Name: r.Name, Models: append([]string(nil), r.MatchModels...), Strategy: strategyOrdered, Failover: defaultFailover()}
		if len(rule.Models) == 0 {
			rule.Models = []string{"*"}
		}
		for _, c := range r.Candidates {
			if c != nil {
				rule.Candidates = append(rule.Candidates, &PolicyCandidate{ID: c.ID, Name: c.Name, ResourceID: c.ResourceID, ResourceKind: c.ResourceKind, Provider: c.Provider, AuthID: c.AuthID, AuthIndex: c.AuthIndex, OverrideModel: c.OverrideModel, Enabled: c.Enabled, Priority: 100, Weight: 1})
			}
		}
		p.Rules = append(p.Rules, rule)
	}
	normalizePolicies(&st)
	return st
}

func normalizePolicies(st *V4State) {
	if st == nil {
		return
	}
	for fp, p := range st.Policies {
		if p == nil {
			delete(st.Policies, fp)
			continue
		}
		if p.KeyFingerprint == "" {
			p.KeyFingerprint = fp
		}
		if p.Name == "" {
			p.Name = "策略 " + p.KeyHint
		}
		for _, r := range p.Rules {
			normalizeRule(r)
		}
	}
}

func normalizeRule(r *PolicyRule) {
	if r == nil {
		return
	}
	if r.ID == "" {
		r.ID = "rule-" + randomShort()
	}
	if r.Name == "" {
		r.Name = r.ID
	}
	r.Models = normalizeV4Models(r.Models)
	switch r.Strategy {
	case strategyOrdered, strategyRoundRobin, strategyWeightedRR, strategyPriorityWeighted, strategySticky, strategyCPADefault:
	default:
		r.Strategy = strategyOrdered
	}
	if r.StickySource == "" {
		r.StickySource = "auto"
	}
	r.Failover = normalizeFailover(r.Failover)
	for _, c := range r.Candidates {
		if c != nil {
			if c.ID == "" {
				c.ID = "cand-" + randomShort()
			}
			if c.Priority == 0 {
				c.Priority = 100
			}
			if c.Weight <= 0 {
				c.Weight = 1
			}
			if c.Weight > 10000 {
				c.Weight = 10000
			}
			c.OverrideModel = strings.TrimSpace(c.OverrideModel)
		}
	}
}

func normalizeV4Models(models []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, raw := range models {
		for _, p := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || r == '\n' }) {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			k := strings.ToLower(p)
			if !seen[k] {
				seen[k] = true
				out = append(out, p)
			}
		}
	}
	if len(out) == 0 {
		out = []string{"*"}
	}
	return out
}

func validatePolicy(p *Policy) error {
	if p == nil {
		return errors.New("policy is required")
	}
	if strings.TrimSpace(p.KeyFingerprint) == "" {
		return errors.New("请选择下游 CPA API Key")
	}
	if len(p.Rules) == 0 {
		return errors.New("至少需要一条模型规则")
	}
	exact := map[string]string{}
	catchAll := 0
	for i, r := range p.Rules {
		if r == nil {
			return fmt.Errorf("规则 %d 为空", i+1)
		}
		normalizeRule(r)
		if r.Strategy != strategyCPADefault && len(enabledV4Candidates(r)) == 0 {
			return fmt.Errorf("规则 %s 没有启用候选", r.Name)
		}
		for _, pat := range r.Models {
			if pat == "*" {
				catchAll++
				if catchAll > 1 {
					return errors.New("一个 Policy 最多只能有一条 * 默认规则")
				}
				continue
			}
			if !strings.ContainsAny(pat, "*?") {
				k := strings.ToLower(pat)
				if prev, ok := exact[k]; ok {
					return fmt.Errorf("模型 %s 同时出现在规则 %s 和 %s 中", pat, prev, r.Name)
				}
				exact[k] = r.Name
			}
		}
	}
	return nil
}

func saveV4State() error {
	runtimeState.RLock()
	path := runtimeState.statePath
	runtimeState.RUnlock()
	v4Runtime.Lock()
	v4Runtime.state.Version = v4StateVersion
	v4Runtime.state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	raw, err := json.MarshalIndent(v4Runtime.state, "", "  ")
	v4Runtime.Unlock()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err = os.WriteFile(tmp, raw, 0600); err != nil {
		return err
	}
	if err = os.Chmod(tmp, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
