package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func observeV4(ev RoutingEvent, callbackID string) {
	if ev.At == "" { ev.At = nowV4() }
	if ev.TraceID == "" { ev.TraceID = traceIDV4() }
	o := obsV4()
	if o.MemoryEnabled {
		v4Runtime.Lock(); v4Runtime.recent = append(v4Runtime.recent, ev); if len(v4Runtime.recent) > o.MemoryLimit { v4Runtime.recent = append([]RoutingEvent(nil), v4Runtime.recent[len(v4Runtime.recent)-o.MemoryLimit:]...) }; v4Runtime.Unlock()
	}
	if o.LogEnabled {
		fields := map[string]any{"decision": ev.Decision, "trace_id": ev.TraceID, "policy": ev.PolicyName, "rule": ev.RuleName, "strategy": ev.Strategy, "model": ev.Model, "final": ev.Final, "provider": ev.Provider, "auth_index": ev.AuthIndex, "status": ev.Status, "attempts": len(ev.Attempts), "duration_ms": ev.DurationMs, "reason": ev.Reason}
		_, _ = callHost("host.log", map[string]any{"host_callback_id": callbackID, "level": o.LogLevel, "message": "kcr routing decision", "fields": fields})
	}
	enqueueSQLiteV4(ev)
}

func obsV4() ObservabilityConfig { v4Runtime.RLock(); defer v4Runtime.RUnlock(); return normalizeObservability(v4Runtime.state.Observability) }
func nowV4() string { return time.Now().UTC().Format(time.RFC3339Nano) }
func traceIDV4() string { return randomShort() + randomShort() + randomShort() }

func restartSQLiteSinkV4() error {
	v4Runtime.Lock(); old := v4Runtime.sqlite; v4Runtime.sqlite = nil; o := normalizeObservability(v4Runtime.state.Observability); v4Runtime.Unlock()
	if old != nil { old.close() }
	if !o.SQLiteEnabled { return nil }
	runtimeState.RLock(); pluginDir := runtimeState.pluginDir; runtimeState.RUnlock(); path := o.SQLitePath; if !filepath.IsAbs(path) { path = filepath.Join(pluginDir, path) }
	sink, err := newSQLiteSinkV4(path, o); if err != nil { return err }; v4Runtime.Lock(); v4Runtime.sqlite = sink; v4Runtime.Unlock(); return nil
}

func shutdownObservabilityV4() { v4Runtime.Lock(); s := v4Runtime.sqlite; v4Runtime.sqlite = nil; v4Runtime.Unlock(); if s != nil { s.close() } }

func enqueueSQLiteV4(ev RoutingEvent) {
	v4Runtime.RLock(); s := v4Runtime.sqlite; enabled := v4Runtime.state.Observability.SQLiteEnabled; v4Runtime.RUnlock(); if !enabled || s == nil { return }
	select { case s.ch <- ev: default: _, _ = callHost("host.log", map[string]any{"level": "warn", "message": "kcr sqlite queue full; routing event dropped", "fields": map[string]any{"trace_id": ev.TraceID}}) }
}

func newSQLiteSinkV4(path string, cfg ObservabilityConfig) (*sqliteSink, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { return nil, err }
	db, err := sql.Open("sqlite3", path+"?_busy_timeout=5000&_journal_mode=WAL&_synchronous=NORMAL"); if err != nil { return nil, err }
	schema := `CREATE TABLE IF NOT EXISTS routing_events (trace_id TEXT PRIMARY KEY, at TEXT NOT NULL, decision TEXT, reason TEXT, policy_name TEXT, key_fingerprint TEXT, key_hint TEXT, rule_id TEXT, rule_name TEXT, strategy TEXT, model TEXT, stream INTEGER, final_resource TEXT, provider TEXT, auth_index TEXT, status INTEGER, duration_ms INTEGER, success INTEGER); CREATE TABLE IF NOT EXISTS routing_attempts (id INTEGER PRIMARY KEY AUTOINCREMENT, trace_id TEXT NOT NULL, sequence INTEGER, candidate TEXT, provider TEXT, auth_index TEXT, model TEXT, status INTEGER, error TEXT, duration_ms INTEGER); CREATE INDEX IF NOT EXISTS idx_routing_events_at ON routing_events(at); CREATE INDEX IF NOT EXISTS idx_routing_attempts_trace ON routing_attempts(trace_id);`
	if _, err = db.Exec(schema); err != nil { _ = db.Close(); return nil, err }
	s := &sqliteSink{db: db, path: path, ch: make(chan RoutingEvent, 1024), stop: make(chan struct{}), done: make(chan struct{}), cfg: cfg}; go s.loop(); return s, nil
}

func (s *sqliteSink) close() {
	if s == nil { return }
	s.mu.Lock(); select { case <-s.stop: s.mu.Unlock(); <-s.done; return; default: close(s.stop) }; s.mu.Unlock(); <-s.done
}

func (s *sqliteSink) loop() {
	defer close(s.done); defer s.db.Close(); ticker := time.NewTicker(time.Minute); defer ticker.Stop(); count := 0
	for { select { case ev := <-s.ch: _ = s.insert(ev); count++; if count%100 == 0 { s.cleanup() }; case <-ticker.C: s.cleanup(); case <-s.stop: for { select { case ev := <-s.ch: _ = s.insert(ev); default: s.cleanup(); return } } } }
}

func (s *sqliteSink) insert(ev RoutingEvent) error {
	tx, err := s.db.Begin(); if err != nil { return err }; committed := false; defer func(){ if !committed { _ = tx.Rollback() } }()
	_, err = tx.Exec(`INSERT OR REPLACE INTO routing_events(trace_id,at,decision,reason,policy_name,key_fingerprint,key_hint,rule_id,rule_name,strategy,model,stream,final_resource,provider,auth_index,status,duration_ms,success) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, ev.TraceID, ev.At, ev.Decision, ev.Reason, ev.PolicyName, ev.KeyFingerprint, ev.KeyHint, ev.RuleID, ev.RuleName, ev.Strategy, ev.Model, boolIntV4(ev.Stream), ev.Final, ev.Provider, ev.AuthIndex, ev.Status, ev.DurationMs, boolIntV4(ev.Success)); if err != nil { return err }
	if _, err = tx.Exec(`DELETE FROM routing_attempts WHERE trace_id=?`, ev.TraceID); err != nil { return err }
	for i, a := range ev.Attempts { if _, err = tx.Exec(`INSERT INTO routing_attempts(trace_id,sequence,candidate,provider,auth_index,model,status,error,duration_ms) VALUES(?,?,?,?,?,?,?,?,?)`, ev.TraceID, i+1, a.Candidate, a.Provider, a.AuthIndex, a.Model, a.Status, a.Error, a.DurationMs); err != nil { return err } }
	if err = tx.Commit(); err != nil { return err }; committed = true; return nil
}

func (s *sqliteSink) cleanup() {
	if s == nil || s.db == nil { return }
	cut := time.Now().UTC().AddDate(0, 0, -s.cfg.SQLiteRetentionDays).Format(time.RFC3339Nano); _, _ = s.db.Exec(`DELETE FROM routing_attempts WHERE trace_id IN (SELECT trace_id FROM routing_events WHERE at < ?)`, cut); _, _ = s.db.Exec(`DELETE FROM routing_events WHERE at < ?`, cut)
	if s.cfg.SQLiteMaxRows > 0 { _, _ = s.db.Exec(`DELETE FROM routing_attempts WHERE trace_id IN (SELECT trace_id FROM routing_events ORDER BY at DESC LIMIT -1 OFFSET ?)`, s.cfg.SQLiteMaxRows); _, _ = s.db.Exec(`DELETE FROM routing_events WHERE trace_id IN (SELECT trace_id FROM routing_events ORDER BY at DESC LIMIT -1 OFFSET ?)`, s.cfg.SQLiteMaxRows) }
}

func boolIntV4(b bool) int { if b { return 1 }; return 0 }
func cloneV4State(s V4State) V4State { b, _ := json.Marshal(s); var out V4State; _ = json.Unmarshal(b, &out); if out.Policies == nil { out.Policies = map[string]*Policy{} }; return out }
func clonePolicyV4(p *Policy) *Policy { if p == nil { return nil }; b, _ := json.Marshal(p); var out Policy; _ = json.Unmarshal(b, &out); return &out }
func cloneRuleV4(r *PolicyRule) *PolicyRule { if r == nil { return nil }; b, _ := json.Marshal(r); var out PolicyRule; _ = json.Unmarshal(b, &out); return &out }
