from pathlib import Path


def replace_once(path, old, new):
    p = Path(path)
    s = p.read_text()
    if old not in s:
        raise SystemExit(f'pattern not found in {path}: {old[:120]!r}')
    p.write_text(s.replace(old, new, 1))

# 1) Scheduler: retain CPA candidate eligibility filtering before resolving live AuthID.
replace_once('src/main.go', '''\tprovider := strings.TrimSpace(rec.Provider)\n\tif provider == "" {\n\t\tprovider = stringAny(req, "Provider")\n\t\tif provider == "" {\n\t\t\tprovider = stringAny(req, "provider")\n\t\t}\n\t}\n\tauthID, err := resolveAuthIDByIndex(rec.AuthIndex, provider)\n''', '''\tprovider := strings.TrimSpace(rec.Provider)\n\tif provider == "" {\n\t\tprovider = stringAny(req, "Provider")\n\t\tif provider == "" {\n\t\t\tprovider = stringAny(req, "provider")\n\t\t}\n\t}\n\tcandidates := anySlice(req["Candidates"])\n\tif len(candidates) == 0 {\n\t\tcandidates = anySlice(req["candidates"])\n\t}\n\tif !schedulerCandidateEligible(candidates, rec.AuthIndex, provider) {\n\t\treturn okEnvelope(map[string]any{"Handled": true, "AuthID": "", "Reason": "kcr_candidate_ineligible"})\n\t}\n\tauthID, err := resolveAuthIDByIndex(rec.AuthIndex, provider)\n''')

p = Path('src/scheduler_auth.go')
s = p.read_text()
old = '''\t// AuthIndex should normally be unique. If a host returns duplicates, use\n\t// provider/type only as a disambiguator instead of allowing an arbitrary pick.\n\tfor _, entry := range matches {\n\t\tentryProvider := strings.TrimSpace(entry.Provider)\n\t\tif entryProvider == "" {\n\t\t\tentryProvider = strings.TrimSpace(entry.Type)\n\t\t}\n\t\tif provider != "" && strings.EqualFold(entryProvider, provider) {\n\t\t\treturn strings.TrimSpace(entry.ID)\n\t\t}\n\t}\n\treturn ""\n}\n'''
new = '''\t// AuthIndex should normally be unique. If a host returns duplicates, use\n\t// provider/type only as a disambiguator and fail closed unless that leaves\n\t// exactly one unique AuthID.\n\tmatchedID := ""\n\tfor _, entry := range matches {\n\t\tentryProvider := strings.TrimSpace(entry.Provider)\n\t\tif entryProvider == "" {\n\t\t\tentryProvider = strings.TrimSpace(entry.Type)\n\t\t}\n\t\tif provider == "" || !strings.EqualFold(entryProvider, provider) {\n\t\t\tcontinue\n\t\t}\n\t\tid := strings.TrimSpace(entry.ID)\n\t\tif matchedID == "" {\n\t\t\tmatchedID = id\n\t\t\tcontinue\n\t\t}\n\t\tif id != matchedID {\n\t\t\treturn ""\n\t\t}\n\t}\n\treturn matchedID\n}\n\nfunc schedulerCandidateEligible(candidates []any, authIndex, provider string) bool {\n\tauthIndex = strings.TrimSpace(authIndex)\n\tprovider = strings.TrimSpace(provider)\n\tif authIndex == "" {\n\t\treturn false\n\t}\n\tfor _, raw := range candidates {\n\t\tm := anyMap(raw)\n\t\tidx := stringAny(m, "AuthIndex")\n\t\tif idx == "" {\n\t\t\tidx = stringAny(m, "auth_index")\n\t\t}\n\t\tif strings.TrimSpace(idx) != authIndex {\n\t\t\tcontinue\n\t\t}\n\t\tcandidateProvider := stringAny(m, "Provider")\n\t\tif candidateProvider == "" {\n\t\t\tcandidateProvider = stringAny(m, "provider")\n\t\t}\n\t\tif provider != "" && !strings.EqualFold(strings.TrimSpace(candidateProvider), provider) {\n\t\t\tcontinue\n\t\t}\n\t\treturn true\n\t}\n\treturn false\n}\n'''
if old not in s:
    raise SystemExit('scheduler_auth duplicate-match block not found')
p.write_text(s.replace(old, new, 1))

# 2) Keep the actual rule snapshot used by an in-flight request.
replace_once('src/v4_types.go', '''\tError            string          `json:"error,omitempty"`\n}\n''', '''\tError            string          `json:"error,omitempty"`\n\truleSnapshot     *PolicyRule\n}\n''')
replace_once('src/v4_execution.go', '''\tevent := RoutingEvent{TraceID: trace, At: nowV4(), Decision: decisionHandled, PolicyName: p.Name, KeyFingerprint: p.KeyFingerprint, KeyHint: p.KeyHint, RuleID: r.ID, RuleName: r.Name, Strategy: r.Strategy, Model: clientModel, Stream: false}\n''', '''\tevent := RoutingEvent{TraceID: trace, At: nowV4(), Decision: decisionHandled, PolicyName: p.Name, KeyFingerprint: p.KeyFingerprint, KeyHint: p.KeyHint, RuleID: r.ID, RuleName: r.Name, Strategy: r.Strategy, Model: clientModel, Stream: false, ruleSnapshot: cloneRuleV4(r)}\n''')
replace_once('src/v4_execution.go', '''\tevent := RoutingEvent{TraceID: trace, At: nowV4(), Decision: decisionHandled, PolicyName: p.Name, KeyFingerprint: p.KeyFingerprint, KeyHint: p.KeyHint, RuleID: r.ID, RuleName: r.Name, Strategy: r.Strategy, Model: clientModel, Stream: true}\n''', '''\tevent := RoutingEvent{TraceID: trace, At: nowV4(), Decision: decisionHandled, PolicyName: p.Name, KeyFingerprint: p.KeyFingerprint, KeyHint: p.KeyHint, RuleID: r.ID, RuleName: r.Name, Strategy: r.Strategy, Model: clientModel, Stream: true, ruleSnapshot: cloneRuleV4(r)}\n''')
replace_once('src/v4_observability.go', '''func ruleForRoutingEventV6(ev *RoutingEvent) *PolicyRule {\n\tif ev == nil || ev.KeyFingerprint == "" {\n\t\treturn nil\n\t}\n''', '''func ruleForRoutingEventV6(ev *RoutingEvent) *PolicyRule {\n\tif ev == nil {\n\t\treturn nil\n\t}\n\tif ev.ruleSnapshot != nil {\n\t\treturn cloneRuleV4(ev.ruleSnapshot)\n\t}\n\tif ev.KeyFingerprint == "" {\n\t\treturn nil\n\t}\n''')

# 3) Migration errors: inspect schema and propagate real failures instead of swallowing ALTER errors.
replace_once('src/v4_observability.go', '''\t// Existing v0.4/v0.5 databases do not have selection_reasons.\n\t// SQLite lacks ADD COLUMN IF NOT EXISTS, so duplicate-column is intentionally ignored.\n\t_, _ = db.Exec(`ALTER TABLE routing_events ADD COLUMN selection_reasons TEXT`)\n\t_, _ = db.Exec(`ALTER TABLE routing_events ADD COLUMN error TEXT`)\n\n\t// v0.6 deliberately stops retaining requests from API keys with no configured Policy.\n\t// Purge historical rows with that old reason as well.\n\t_, _ = db.Exec(`DELETE FROM routing_attempts WHERE trace_id IN (SELECT trace_id FROM routing_events WHERE reason='no_policy')`)\n\t_, _ = db.Exec(`DELETE FROM routing_events WHERE reason='no_policy'`)\n''', '''\t// Existing v0.4/v0.5 databases may not have columns introduced later.\n\t// Inspect the schema first so only an actually missing column is altered; all\n\t// genuine migration failures are propagated to the caller.\n\tif err = ensureRoutingEventColumnV4(db, "selection_reasons", "TEXT"); err != nil {\n\t\t_ = db.Close()\n\t\treturn nil, fmt.Errorf("migrate routing_events.selection_reasons: %w", err)\n\t}\n\tif err = ensureRoutingEventColumnV4(db, "error", "TEXT"); err != nil {\n\t\t_ = db.Close()\n\t\treturn nil, fmt.Errorf("migrate routing_events.error: %w", err)\n\t}\n\n\t// v0.6 deliberately stops retaining requests from API keys with no configured Policy.\n\t// Purge historical rows with that old reason as well.\n\tif _, err = db.Exec(`DELETE FROM routing_attempts WHERE trace_id IN (SELECT trace_id FROM routing_events WHERE reason='no_policy')`); err != nil {\n\t\t_ = db.Close()\n\t\treturn nil, fmt.Errorf("purge no_policy attempts: %w", err)\n\t}\n\tif _, err = db.Exec(`DELETE FROM routing_events WHERE reason='no_policy'`); err != nil {\n\t\t_ = db.Close()\n\t\treturn nil, fmt.Errorf("purge no_policy events: %w", err)\n\t}\n''')

insert_anchor = '''func (s *sqliteSink) close() {\n'''
helper = '''func ensureRoutingEventColumnV4(db *sql.DB, column, decl string) error {\n\tif db == nil {\n\t\treturn errors.New("sqlite db is nil")\n\t}\n\tswitch column {\n\tcase "selection_reasons", "error":\n\tdefault:\n\t\treturn fmt.Errorf("unsupported routing_events column %q", column)\n\t}\n\trows, err := db.Query(`PRAGMA table_info(routing_events)`)\n\tif err != nil {\n\t\treturn err\n\t}\n\tfound := false\n\tfor rows.Next() {\n\t\tvar cid, notNull, pk int\n\t\tvar name, typ string\n\t\tvar defaultValue any\n\t\tif err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {\n\t\t\trows.Close()\n\t\t\treturn err\n\t\t}\n\t\tif name == column {\n\t\t\tfound = true\n\t\t}\n\t}\n\tif err := rows.Err(); err != nil {\n\t\trows.Close()\n\t\treturn err\n\t}\n\tif err := rows.Close(); err != nil {\n\t\treturn err\n\t}\n\tif found {\n\t\treturn nil\n\t}\n\t_, err = db.Exec(`ALTER TABLE routing_events ADD COLUMN ` + column + ` ` + decl)\n\treturn err\n}\n\n'''
p = Path('src/v4_observability.go')
s = p.read_text()
if insert_anchor not in s:
    raise SystemExit('sqlite close anchor not found')
s = s.replace(insert_anchor, helper + insert_anchor, 1)
p.write_text(s)

# 4) SQLite writer health is sticky-degraded after any lost write; queries fall back to memory until sink restart.
p = Path('src/v062_sqlite_query.go')
s = p.read_text()
s = s.replace('import (\n    "database/sql"', 'import (\n    "context"\n    "database/sql"', 1)
s = s.replace('''    LastError   string\n    LastErrorAt string\n}{}''', '''    LastError    string\n    LastErrorAt  string\n    WriteHealthy bool\n}{}''', 1)
s = s.replace('''    sqliteHealthV62.LastError = ""\n    sqliteHealthV62.LastErrorAt = ""\n    sqliteHealthV62.Unlock()\n}''', '''    sqliteHealthV62.LastError = ""\n    sqliteHealthV62.LastErrorAt = ""\n    sqliteHealthV62.WriteHealthy = false\n    sqliteHealthV62.Unlock()\n}''', 1)
s = s.replace('''    sqliteHealthV62.LastError = ""\n    sqliteHealthV62.LastErrorAt = ""\n    sqliteHealthV62.Unlock()\n    _ = path\n}''', '''    sqliteHealthV62.LastError = ""\n    sqliteHealthV62.LastErrorAt = ""\n    sqliteHealthV62.WriteHealthy = true\n    sqliteHealthV62.Unlock()\n    _ = path\n}''', 1)
old = '''func recordSQLiteWriteV62(err error) {\n    if err == nil {\n        sqliteHealthV62.Lock()\n        sqliteHealthV62.LastWriteAt = nowV4()\n        sqliteHealthV62.LastError = ""\n        sqliteHealthV62.LastErrorAt = ""\n        sqliteHealthV62.Unlock()\n        return\n    }\n    recordSQLiteOpenErrorV62(err)\n    _, _ = callHost("host.log", map[string]any{\n        "level": "error",\n        "message": "kcr sqlite write failed",\n        "fields": map[string]any{"error": err.Error()},\n    })\n}\n'''
new = '''func recordSQLiteWriteV62(err error) {\n    if err == nil {\n        sqliteHealthV62.Lock()\n        sqliteHealthV62.LastWriteAt = nowV4()\n        // Once an event has been lost, keep the writer degraded until the sink is\n        // restarted. Later successful inserts cannot backfill the missing event.\n        if sqliteHealthV62.WriteHealthy {\n            sqliteHealthV62.LastError = ""\n            sqliteHealthV62.LastErrorAt = ""\n        }\n        sqliteHealthV62.Unlock()\n        return\n    }\n    sqliteHealthV62.Lock()\n    sqliteHealthV62.WriteHealthy = false\n    sqliteHealthV62.LastError = err.Error()\n    sqliteHealthV62.LastErrorAt = nowV4()\n    sqliteHealthV62.Unlock()\n    _, _ = callHost("host.log", map[string]any{\n        "level": "error",\n        "message": "kcr sqlite write failed; routing history queries will use memory until sqlite restarts",\n        "fields": map[string]any{"error": err.Error()},\n    })\n}\n\nfunc sqliteWriterHealthyV62() bool {\n    sqliteHealthV62.RLock()\n    healthy := sqliteHealthV62.WriteHealthy\n    sqliteHealthV62.RUnlock()\n    return healthy\n}\n'''
if old not in s:
    raise SystemExit('recordSQLiteWriteV62 block not found')
s = s.replace(old, new, 1)
s = s.replace('''    lastError := sqliteHealthV62.LastError\n    lastErrorAt := sqliteHealthV62.LastErrorAt\n    sqliteHealthV62.RUnlock()\n''', '''    lastError := sqliteHealthV62.LastError\n    lastErrorAt := sqliteHealthV62.LastErrorAt\n    writeHealthy := sqliteHealthV62.WriteHealthy\n    sqliteHealthV62.RUnlock()\n''', 1)
s = s.replace('''        "last_error_at": lastErrorAt,\n        "events": 0,''', '''        "last_error_at": lastErrorAt,\n        "write_healthy": writeHealthy,\n        "events": 0,''', 1)
s = s.replace('''    if !obs.SQLiteEnabled || sink == nil || sink.db == nil {\n        return nil, false\n    }\n''', '''    if !obs.SQLiteEnabled || sink == nil || sink.db == nil || !sqliteWriterHealthyV62() {\n        return nil, false\n    }\n''', 1)

# 5) All SQLite reads that make one explorer response use one read transaction/snapshot.
old_start = '''func querySQLiteEventsV62(db *sql.DB, q url.Values, obs ObservabilityConfig) (map[string]any, error) {\n    where, args := sqliteWhereV62(q)\n    limit := parseEventLimitV6(q.Get("limit"))\n'''
new_start = '''type sqliteQueryerV62 interface {\n    Query(query string, args ...any) (*sql.Rows, error)\n    QueryRow(query string, args ...any) *sql.Row\n}\n\nfunc querySQLiteEventsV62(db *sql.DB, q url.Values, obs ObservabilityConfig) (map[string]any, error) {\n    tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})\n    if err != nil {\n        return nil, err\n    }\n    defer tx.Rollback()\n\n    where, args := sqliteWhereV62(q)\n    limit := parseEventLimitV6(q.Get("limit"))\n'''
if old_start not in s:
    raise SystemExit('querySQLiteEventsV62 start not found')
s = s.replace(old_start, new_start, 1)
# Restrict replacements to query function body before sqliteWhere.
start = s.index('func querySQLiteEventsV62')
end = s.index('func sqliteWhereV62', start)
body = s[start:end]
body = body.replace('db.QueryRow(', 'tx.QueryRow(').replace('db.Query(', 'tx.Query(').replace('sqliteFacetsV62(db)', 'sqliteFacetsV62(tx)')
old_return = '''    successRate := float64(0)\n    if total > 0 {\n        successRate = float64(success) * 100 / float64(total)\n    }\n    return map[string]any{'''
new_return = '''    if err := tx.Commit(); err != nil {\n        return nil, err\n    }\n    successRate := float64(0)\n    if total > 0 {\n        successRate = float64(success) * 100 / float64(total)\n    }\n    return map[string]any{'''
if old_return not in body:
    raise SystemExit('querySQLiteEventsV62 return anchor not found')
body = body.replace(old_return, new_return, 1)
s = s[:start] + body + s[end:]
s = s.replace('func sqliteFacetsV62(db *sql.DB)', 'func sqliteFacetsV62(db sqliteQueryerV62)', 1)
s = s.replace('func sqliteFacetV62(db *sql.DB, query string)', 'func sqliteFacetV62(db sqliteQueryerV62, query string)', 1)
p.write_text(s)

# Mark queue overflow as a persistence loss as well as logging it.
replace_once('src/v4_observability.go', '''\tdefault:\n\t\t_, _ = callHost("host.log", map[string]any{"level": "warn", "message": "kcr sqlite queue full; routing event dropped", "fields": map[string]any{"trace_id": ev.TraceID}})\n\t}\n}\n''', '''\tdefault:\n\t\terr := fmt.Errorf("sqlite queue full; routing event %s dropped", ev.TraceID)\n\t\trecordSQLiteWriteV62(err)\n\t\t_, _ = callHost("host.log", map[string]any{"level": "warn", "message": "kcr sqlite queue full; routing event dropped", "fields": map[string]any{"trace_id": ev.TraceID}})\n\t}\n}\n''')

# 6) Mobile expanded detail restores all fields hidden by the compact responsive table.
p = Path('src/ui.js')
s = p.read_text()
old = '''function renderEventDetail(e) {\n  const eventReason = reasonLabel(e.reason);\n  const details = [\n    e.decision ? 'Decision ' + e.decision : '',\n    e.auth_index ? '最终 AuthIndex ' + e.auth_index : '',\n    e.key_hint ? 'API Key ' + e.key_hint : '',\n    e.trace_id ? 'Trace ' + e.trace_id : ''\n  ].filter(Boolean).join(' · ');\n  return '<div class="event-detail">' +\n    (eventReason ? '<div class="event-detail-reason"><b>路由结果：</b>' + esc(eventReason) + '</div>' : '') +\n    '<div class="muted small event-detail-meta">' + esc(details) + '</div>' +\n'''
new = '''function renderEventDetail(e) {\n  const eventReason = reasonLabel(e.reason);\n  const final = e.final || (e.decision === 'KCR_FALLBACK_TO_CPA' ? 'CPA Default' : '-');\n  const duration = Number.isFinite(Number(e.duration_ms)) ? e.duration_ms + ' ms' : '-';\n  const primary = [\n    '时间 ' + fmtEventTime(e.at),\n    'Model ' + (e.model || '-'),\n    'Policy ' + (e.policy_name || '-'),\n    'Rule ' + (e.rule_name || '-'),\n    'Strategy ' + (e.strategy || '-'),\n    '最终候选 ' + final,\n    'Provider ' + (e.provider || '-'),\n    '状态 ' + eventStatusText(e),\n    '耗时 ' + duration,\n    'Attempts ' + ((e.attempts || []).length)\n  ].join(' · ');\n  const details = [\n    e.decision ? 'Decision ' + e.decision : '',\n    e.auth_index ? '最终 AuthIndex ' + e.auth_index : '',\n    e.key_hint ? 'API Key ' + e.key_hint : '',\n    e.trace_id ? 'Trace ' + e.trace_id : ''\n  ].filter(Boolean).join(' · ');\n  return '<div class="event-detail">' +\n    '<div class="small event-detail-meta">' + esc(primary) + '</div>' +\n    (eventReason ? '<div class="event-detail-reason"><b>路由结果：</b>' + esc(eventReason) + '</div>' : '') +\n    '<div class="muted small event-detail-meta">' + esc(details) + '</div>' +\n'''
if old not in s:
    raise SystemExit('renderEventDetail block not found')
s = s.replace(old, new, 1)
# Surface degraded writer health explicitly.
s = s.replace("'<div class=\"' + cls + '\"><b>SQLite ' + (h.active ? '运行中' : '未运行') + '</b>", "'<div class=\"' + cls + '\"><b>SQLite ' + (h.active ? (h.write_healthy === false ? '运行中（写入异常，查询回退 Memory）' : '运行中') : '未运行') + '</b>", 1)
p.write_text(s)

# 7) Tests for all behavioral findings.
Path('src/review_findings_test.go').write_text(r'''package main

import (
    "database/sql"
    "net/url"
    "strings"
    "testing"
    "time"

    _ "github.com/mattn/go-sqlite3"
)

func TestSchedulerCandidateEligibleRequiresMembership(t *testing.T) {
    candidates := []any{
        map[string]any{"auth_index": "idx-a", "provider": "codex"},
    }
    if !schedulerCandidateEligible(candidates, "idx-a", "codex") {
        t.Fatal("expected matching scheduler candidate to be eligible")
    }
    if schedulerCandidateEligible(candidates, "idx-b", "codex") {
        t.Fatal("ticketed auth index absent from Candidates must be rejected")
    }
    if schedulerCandidateEligible(candidates, "idx-a", "claude") {
        t.Fatal("provider-mismatched scheduler candidate must be rejected")
    }
    if schedulerCandidateEligible(nil, "idx-a", "codex") {
        t.Fatal("empty Candidates must be rejected")
    }
}

func TestAuthIDFromEntriesRejectsDuplicateProviderMatches(t *testing.T) {
    entries := []hostAuthEntry{
        {ID: "auth-a", AuthIndex: "idx", Provider: "codex"},
        {ID: "auth-b", AuthIndex: "idx", Provider: "codex"},
    }
    if got := authIDFromEntries(entries, "idx", "codex"); got != "" {
        t.Fatalf("ambiguous provider matches must fail closed, got %q", got)
    }
    entries[1].ID = "auth-a"
    if got := authIDFromEntries(entries, "idx", "codex"); got != "auth-a" {
        t.Fatalf("duplicate rows for the same AuthID are safe, got %q", got)
    }
}

func TestSelectionReasonsUseRequestRuleSnapshot(t *testing.T) {
    original := &PolicyRule{
        ID: "r1", Strategy: strategyPriorityWeighted,
        Candidates: []*PolicyCandidate{{ID: "c1", Name: "A", AuthIndex: "idx", Provider: "codex", Priority: 100, Weight: 3, Enabled: true}},
        Failover: FailoverPolicy{RateLimit: failNext},
    }
    ev := RoutingEvent{
        RuleID: "r1", RuleName: "rule", Strategy: strategyPriorityWeighted,
        ruleSnapshot: cloneRuleV4(original),
        Attempts: []attemptResult{{Candidate: "A", AuthIndex: "idx", Provider: "codex", Status: 200}},
    }

    // Simulate a concurrent edit after the request started.
    v4Runtime.Lock()
    old := v4Runtime.state
    v4Runtime.state = V4State{Policies: map[string]*Policy{}}
    v4Runtime.Unlock()
    defer func() {
        v4Runtime.Lock()
        v4Runtime.state = old
        v4Runtime.Unlock()
    }()

    enrichRoutingSelectionReasonsV6(&ev)
    if len(ev.SelectionReasons) != 1 || !strings.Contains(ev.SelectionReasons[0], "Weight=3") {
        t.Fatalf("selection reason must use request snapshot, got %#v", ev.SelectionReasons)
    }
}

func TestEnsureRoutingEventColumnV4IsIdempotent(t *testing.T) {
    db, err := sql.Open("sqlite3", ":memory:")
    if err != nil { t.Fatal(err) }
    defer db.Close()
    if _, err := db.Exec(`CREATE TABLE routing_events (trace_id TEXT PRIMARY KEY, at TEXT NOT NULL)`); err != nil { t.Fatal(err) }
    if err := ensureRoutingEventColumnV4(db, "selection_reasons", "TEXT"); err != nil { t.Fatal(err) }
    if err := ensureRoutingEventColumnV4(db, "selection_reasons", "TEXT"); err != nil { t.Fatalf("second migration should be a no-op: %v", err) }
    if err := ensureRoutingEventColumnV4(db, "bogus", "TEXT"); err == nil { t.Fatal("unexpected column must be rejected") }
}

func TestSQLiteWriteFailureDisablesSQLiteQuerySource(t *testing.T) {
    resetSQLiteHealthV62()
    recordSQLiteOpenV62("test.db")
    if !sqliteWriterHealthyV62() { t.Fatal("fresh sqlite sink should be healthy") }
    recordSQLiteWriteV62(assertErr("disk full"))
    if sqliteWriterHealthyV62() { t.Fatal("write failure must degrade sqlite source") }
    recordSQLiteWriteV62(nil)
    if sqliteWriterHealthyV62() { t.Fatal("degraded state must remain sticky until sink restart") }
}

type assertErr string
func (e assertErr) Error() string { return string(e) }

func TestSQLiteEventQueryUsesConsistentTransaction(t *testing.T) {
    db, err := sql.Open("sqlite3", ":memory:")
    if err != nil { t.Fatal(err) }
    defer db.Close()
    db.SetMaxOpenConns(1)
    schema := `
      CREATE TABLE routing_events (
        trace_id TEXT PRIMARY KEY, at TEXT NOT NULL, decision TEXT, reason TEXT, selection_reasons TEXT,
        policy_name TEXT, key_fingerprint TEXT, key_hint TEXT, rule_id TEXT, rule_name TEXT, strategy TEXT,
        model TEXT, stream INTEGER, final_resource TEXT, provider TEXT, auth_index TEXT, status INTEGER,
        duration_ms INTEGER, success INTEGER, error TEXT
      );
      CREATE TABLE routing_attempts (
        id INTEGER PRIMARY KEY AUTOINCREMENT, trace_id TEXT NOT NULL, sequence INTEGER, candidate TEXT,
        provider TEXT, auth_index TEXT, model TEXT, status INTEGER, error TEXT, duration_ms INTEGER
      );`
    if _, err := db.Exec(schema); err != nil { t.Fatal(err) }
    if _, err := db.Exec(`INSERT INTO routing_events(trace_id,at,decision,reason,success,duration_ms) VALUES(?,?,?,?,?,?)`, "t1", time.Now().UTC().Format(time.RFC3339Nano), decisionHandled, "", 1, 12); err != nil { t.Fatal(err) }
    out, err := querySQLiteEventsV62(db, url.Values{"limit": {"10"}}, ObservabilityConfig{MemoryEnabled: true, MemoryLimit: 500})
    if err != nil { t.Fatal(err) }
    if out["matched"].(int) != out["returned"].(int) {
        t.Fatalf("snapshot response mismatch: matched=%v returned=%v", out["matched"], out["returned"])
    }
}
''')

print('review findings patch applied')
