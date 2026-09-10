package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func observeV4(ev RoutingEvent, callbackID string) {
	if !prepareObservationV6(&ev) {
		return
	}
	if ev.At == "" {
		ev.At = nowV4()
	}
	if ev.TraceID == "" {
		ev.TraceID = traceIDV4()
	}
	enrichRoutingSelectionReasonsV6(&ev)

	o := obsV4()
	if o.MemoryEnabled {
		v4Runtime.Lock()
		v4Runtime.recent = append(v4Runtime.recent, ev)
		if len(v4Runtime.recent) > o.MemoryLimit {
			v4Runtime.recent = append([]RoutingEvent(nil), v4Runtime.recent[len(v4Runtime.recent)-o.MemoryLimit:]...)
		}
		v4Runtime.Unlock()
	}
	if o.LogEnabled {
		fields := map[string]any{
			"decision":          ev.Decision,
			"trace_id":          ev.TraceID,
			"policy":            ev.PolicyName,
			"rule":              ev.RuleName,
			"strategy":          ev.Strategy,
			"model":             ev.Model,
			"final":             ev.Final,
			"provider":          ev.Provider,
			"auth_index":        ev.AuthIndex,
			"status":            ev.Status,
			"attempts":          len(ev.Attempts),
			"duration_ms":       ev.DurationMs,
			"reason":            ev.Reason,
			"selection_reasons": ev.SelectionReasons,
		}
		_, _ = callHost("host.log", map[string]any{"host_callback_id": callbackID, "level": o.LogLevel, "message": "kcr routing decision", "fields": fields})
	}
	enqueueSQLiteV4(ev)
}

// prepareObservationV6 drops requests from API keys that have no KCR Policy at all.
// A configured-but-disabled Policy remains observable and is labeled separately.
func prepareObservationV6(ev *RoutingEvent) bool {
	if ev == nil {
		return false
	}
	if ev.Reason != "no_policy" {
		return true
	}

	v4Runtime.RLock()
	p := v4Runtime.state.Policies[ev.KeyFingerprint]
	var name, hint string
	var enabled bool
	if p != nil {
		name = p.Name
		hint = p.KeyHint
		enabled = p.Enabled
	}
	v4Runtime.RUnlock()

	if p == nil {
		return false
	}
	ev.PolicyName = name
	if ev.KeyHint == "" {
		ev.KeyHint = hint
	}
	if !enabled {
		ev.Reason = "policy_disabled"
		return true
	}

	// An enabled Policy normally cannot produce no_policy. Keep the anomaly rather
	// than silently discarding a possible concurrent state/configuration race.
	ev.Reason = "policy_lookup_miss"
	return true
}

func enrichRoutingSelectionReasonsV6(ev *RoutingEvent) {
	if ev == nil || len(ev.Attempts) == 0 {
		return
	}
	rule := ruleForRoutingEventV6(ev)
	reasons := make([]string, len(ev.Attempts))
	for i := range ev.Attempts {
		if i == 0 {
			reasons[i] = initialSelectionReasonV6(rule, ev.Attempts[i])
			continue
		}
		reasons[i] = failoverSelectionReasonV6(ev, rule, i)
	}
	ev.SelectionReasons = reasons
}

func ruleForRoutingEventV6(ev *RoutingEvent) *PolicyRule {
	if ev == nil || ev.KeyFingerprint == "" {
		return nil
	}
	v4Runtime.RLock()
	p := v4Runtime.state.Policies[ev.KeyFingerprint]
	if p == nil {
		v4Runtime.RUnlock()
		return nil
	}
	for _, r := range p.Rules {
		if r == nil {
			continue
		}
		if (ev.RuleID != "" && r.ID == ev.RuleID) || (ev.RuleID == "" && ev.RuleName != "" && r.Name == ev.RuleName) {
			out := cloneRuleV4(r)
			v4Runtime.RUnlock()
			return out
		}
	}
	v4Runtime.RUnlock()
	return nil
}

func candidateForAttemptV6(rule *PolicyRule, a attemptResult) *PolicyCandidate {
	if rule == nil {
		return nil
	}
	for _, c := range rule.Candidates {
		if c == nil {
			continue
		}
		if a.AuthIndex != "" && c.AuthIndex == a.AuthIndex {
			cc := *c
			return &cc
		}
	}
	for _, c := range rule.Candidates {
		if c == nil {
			continue
		}
		if a.Candidate != "" && c.Name == a.Candidate && (a.Provider == "" || strings.EqualFold(c.Provider, a.Provider)) {
			cc := *c
			return &cc
		}
	}
	return nil
}

func initialSelectionReasonV6(rule *PolicyRule, a attemptResult) string {
	if strings.EqualFold(a.Candidate, "CPA Default") {
		return "直接交由 CPA 默认路由"
	}
	if rule == nil {
		return "按当前策略排序后选择首个可用候选"
	}

	enabled := enabledV4Candidates(rule)
	if len(enabled) == 1 {
		return "仅 1 个启用候选，直接选择"
	}
	c := candidateForAttemptV6(rule, a)

	switch rule.Strategy {
	case strategyOrdered:
		return "有序 Failover：按候选配置顺序选择首个启用候选"
	case strategyRoundRobin:
		return "Round Robin：轮询游标在本轮指向该候选"
	case strategyWeightedRR:
		if c != nil {
			return fmt.Sprintf("Smooth Weighted Round Robin：按 Weight=%d 的平滑权重状态选中", normalizedWeightV6(c.Weight))
		}
		return "Smooth Weighted Round Robin：按平滑权重状态选中"
	case strategyPriorityWeighted:
		if c != nil {
			return fmt.Sprintf("Priority Weighted：先进入最高 Priority=%d 组，再按 Weight=%d 平滑加权选中", normalizedPriorityV6(c.Priority), normalizedWeightV6(c.Weight))
		}
		return "Priority Weighted：先选择最高 Priority 组，再在组内平滑加权"
	case strategySticky:
		source := strings.TrimSpace(rule.StickySource)
		if source == "" {
			source = "auto"
		}
		if c != nil {
			return fmt.Sprintf("Sticky Weighted Rendezvous Hash：来源=%s，Priority=%d，Weight=%d，哈希得分最高", source, normalizedPriorityV6(c.Priority), normalizedWeightV6(c.Weight))
		}
		return fmt.Sprintf("Sticky Weighted Rendezvous Hash：来源=%s，哈希得分最高", source)
	default:
		return "按当前策略排序后选择首个可用候选"
	}
}

func failoverSelectionReasonV6(ev *RoutingEvent, rule *PolicyRule, index int) string {
	if ev == nil || index <= 0 || index >= len(ev.Attempts) {
		return "按 Failover 策略选择后续候选"
	}
	prev := ev.Attempts[index-1]
	curr := ev.Attempts[index]
	cause := attemptFailureSummaryV6(prev)

	var action string
	if rule != nil {
		var err error
		if prev.Error != "" {
			err = errors.New(prev.Error)
		}
		action = failureActionV4(rule.Failover, prev.Status, err)
	}

	if strings.EqualFold(curr.Candidate, "CPA Default") {
		if action == failCPADefault {
			return cause + "；该失败类型配置为 cpa-default，转入 CPA 默认路由"
		}
		return cause + "；候选链已耗尽且 Exhausted=cpa-default，转入 CPA 默认路由"
	}

	switch action {
	case failSamePriorityFirst:
		return cause + "；Failover=same-priority-first，优先选择同 Priority 的未尝试候选"
	case failNextPriority:
		return cause + "；Failover=next-priority，直接降到下一 Priority 的未尝试候选"
	case failNext:
		return cause + "；Failover=next，按当前候选排序选择下一个未尝试候选"
	case failStop:
		return cause + "；Failover=stop（当前记录出现后续候选，可能来自旧版本记录）"
	case failCPADefault:
		return cause + "；Failover=cpa-default"
	default:
		return cause + "；按 Failover 策略选择后续候选"
	}
}

func attemptFailureSummaryV6(a attemptResult) string {
	name := strings.TrimSpace(a.Candidate)
	if name == "" {
		name = "上一候选"
	} else {
		name = "上一候选 " + name
	}
	if a.Error != "" {
		if a.Status > 0 {
			return fmt.Sprintf("%s 执行失败（HTTP %d）", name, a.Status)
		}
		return name + " 发生网络/上游执行错误"
	}
	if a.Status > 0 {
		return fmt.Sprintf("%s 返回 HTTP %d", name, a.Status)
	}
	return name + " 未成功"
}

func normalizedPriorityV6(v int) int {
	if v == 0 {
		return 100
	}
	return v
}

func normalizedWeightV6(v int) int {
	if v <= 0 {
		return 1
	}
	return v
}

func obsV4() ObservabilityConfig {
	v4Runtime.RLock()
	defer v4Runtime.RUnlock()
	return normalizeObservability(v4Runtime.state.Observability)
}

func nowV4() string     { return time.Now().UTC().Format(time.RFC3339Nano) }
func traceIDV4() string { return randomShort() + randomShort() + randomShort() }

func restartSQLiteSinkV4() error {
	v4Runtime.Lock()
	old := v4Runtime.sqlite
	v4Runtime.sqlite = nil
	o := normalizeObservability(v4Runtime.state.Observability)
	v4Runtime.Unlock()
	if old != nil {
		old.close()
	}
	if !o.SQLiteEnabled {
		resetSQLiteHealthV62()
		return nil
	}
	runtimeState.RLock()
	pluginDir := runtimeState.pluginDir
	runtimeState.RUnlock()
	path := o.SQLitePath
	if !filepath.IsAbs(path) {
		path = filepath.Join(pluginDir, path)
	}
	sink, err := newSQLiteSinkV4(path, o)
	if err != nil {
		recordSQLiteOpenErrorV62(err)
		return err
	}
	recordSQLiteOpenV62(path)
	v4Runtime.Lock()
	v4Runtime.sqlite = sink
	v4Runtime.Unlock()
	return nil
}

func shutdownObservabilityV4() {
	v4Runtime.Lock()
	s := v4Runtime.sqlite
	v4Runtime.sqlite = nil
	v4Runtime.Unlock()
	if s != nil {
		s.close()
	}
}

func enqueueSQLiteV4(ev RoutingEvent) {
	v4Runtime.RLock()
	s := v4Runtime.sqlite
	enabled := v4Runtime.state.Observability.SQLiteEnabled
	v4Runtime.RUnlock()
	if !enabled || s == nil {
		return
	}
	select {
	case s.ch <- ev:
	default:
		_, _ = callHost("host.log", map[string]any{"level": "warn", "message": "kcr sqlite queue full; routing event dropped", "fields": map[string]any{"trace_id": ev.TraceID}})
	}
}

func newSQLiteSinkV4(path string, cfg ObservabilityConfig) (*sqliteSink, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", path+"?_busy_timeout=5000&_journal_mode=WAL&_synchronous=NORMAL")
	if err != nil {
		return nil, err
	}
	schema := `CREATE TABLE IF NOT EXISTS routing_events (
		trace_id TEXT PRIMARY KEY,
		at TEXT NOT NULL,
		decision TEXT,
		reason TEXT,
		selection_reasons TEXT,
		policy_name TEXT,
		key_fingerprint TEXT,
		key_hint TEXT,
		rule_id TEXT,
		rule_name TEXT,
		strategy TEXT,
		model TEXT,
		stream INTEGER,
		final_resource TEXT,
		provider TEXT,
		auth_index TEXT,
		status INTEGER,
		duration_ms INTEGER,
		success INTEGER,
		error TEXT
	);
	CREATE TABLE IF NOT EXISTS routing_attempts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		trace_id TEXT NOT NULL,
		sequence INTEGER,
		candidate TEXT,
		provider TEXT,
		auth_index TEXT,
		model TEXT,
		status INTEGER,
		error TEXT,
		duration_ms INTEGER
	);
	CREATE INDEX IF NOT EXISTS idx_routing_events_at ON routing_events(at);
	CREATE INDEX IF NOT EXISTS idx_routing_attempts_trace ON routing_attempts(trace_id);`
	if _, err = db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, err
	}

	// Existing v0.4/v0.5 databases do not have selection_reasons.
	// SQLite lacks ADD COLUMN IF NOT EXISTS, so duplicate-column is intentionally ignored.
	_, _ = db.Exec(`ALTER TABLE routing_events ADD COLUMN selection_reasons TEXT`)
	_, _ = db.Exec(`ALTER TABLE routing_events ADD COLUMN error TEXT`)

	// v0.6 deliberately stops retaining requests from API keys with no configured Policy.
	// Purge historical rows with that old reason as well.
	_, _ = db.Exec(`DELETE FROM routing_attempts WHERE trace_id IN (SELECT trace_id FROM routing_events WHERE reason='no_policy')`)
	_, _ = db.Exec(`DELETE FROM routing_events WHERE reason='no_policy'`)

	s := &sqliteSink{db: db, path: path, ch: make(chan RoutingEvent, 1024), stop: make(chan struct{}), done: make(chan struct{}), cfg: cfg}
	go s.loop()
	return s, nil
}

func (s *sqliteSink) close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	select {
	case <-s.stop:
		s.mu.Unlock()
		<-s.done
		return
	default:
		close(s.stop)
	}
	s.mu.Unlock()
	<-s.done
}

func (s *sqliteSink) loop() {
	defer close(s.done)
	defer s.db.Close()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	count := 0
	for {
		select {
		case ev := <-s.ch:
			recordSQLiteWriteV62(s.insert(ev))
			count++
			if count%100 == 0 {
				s.cleanup()
			}
		case <-ticker.C:
			s.cleanup()
		case <-s.stop:
			for {
				select {
				case ev := <-s.ch:
					recordSQLiteWriteV62(s.insert(ev))
				default:
					s.cleanup()
					return
				}
			}
		}
	}
}

func (s *sqliteSink) insert(ev RoutingEvent) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	reasonsJSON, _ := json.Marshal(ev.SelectionReasons)
	_, err = tx.Exec(
		`INSERT OR REPLACE INTO routing_events(trace_id,at,decision,reason,selection_reasons,policy_name,key_fingerprint,key_hint,rule_id,rule_name,strategy,model,stream,final_resource,provider,auth_index,status,duration_ms,success,error) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		ev.TraceID, ev.At, ev.Decision, ev.Reason, string(reasonsJSON), ev.PolicyName, ev.KeyFingerprint, ev.KeyHint, ev.RuleID, ev.RuleName, ev.Strategy, ev.Model, boolIntV4(ev.Stream), ev.Final, ev.Provider, ev.AuthIndex, ev.Status, ev.DurationMs, boolIntV4(ev.Success), ev.Error,
	)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM routing_attempts WHERE trace_id=?`, ev.TraceID); err != nil {
		return err
	}
	for i, a := range ev.Attempts {
		if _, err = tx.Exec(`INSERT INTO routing_attempts(trace_id,sequence,candidate,provider,auth_index,model,status,error,duration_ms) VALUES(?,?,?,?,?,?,?,?,?)`, ev.TraceID, i+1, a.Candidate, a.Provider, a.AuthIndex, a.Model, a.Status, a.Error, a.DurationMs); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *sqliteSink) cleanup() {
	if s == nil || s.db == nil {
		return
	}
	cut := time.Now().UTC().AddDate(0, 0, -s.cfg.SQLiteRetentionDays).Format(time.RFC3339Nano)
	_, _ = s.db.Exec(`DELETE FROM routing_attempts WHERE trace_id IN (SELECT trace_id FROM routing_events WHERE at < ?)`, cut)
	_, _ = s.db.Exec(`DELETE FROM routing_events WHERE at < ?`, cut)
	if s.cfg.SQLiteMaxRows > 0 {
		_, _ = s.db.Exec(`DELETE FROM routing_attempts WHERE trace_id IN (SELECT trace_id FROM routing_events ORDER BY at DESC LIMIT -1 OFFSET ?)`, s.cfg.SQLiteMaxRows)
		_, _ = s.db.Exec(`DELETE FROM routing_events WHERE trace_id IN (SELECT trace_id FROM routing_events ORDER BY at DESC LIMIT -1 OFFSET ?)`, s.cfg.SQLiteMaxRows)
	}
}

func boolIntV4(b bool) int {
	if b {
		return 1
	}
	return 0
}

func cloneV4State(s V4State) V4State {
	b, _ := json.Marshal(s)
	var out V4State
	_ = json.Unmarshal(b, &out)
	if out.Policies == nil {
		out.Policies = map[string]*Policy{}
	}
	return out
}

func clonePolicyV4(p *Policy) *Policy {
	if p == nil {
		return nil
	}
	b, _ := json.Marshal(p)
	var out Policy
	_ = json.Unmarshal(b, &out)
	return &out
}

func cloneRuleV4(r *PolicyRule) *PolicyRule {
	if r == nil {
		return nil
	}
	b, _ := json.Marshal(r)
	var out PolicyRule
	_ = json.Unmarshal(b, &out)
	return &out
}
