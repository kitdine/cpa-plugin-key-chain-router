from pathlib import Path
import re

ROOT = Path('.')

helper = r'''package main

import (
    "database/sql"
    "encoding/json"
    "net/url"
    "os"
    "sort"
    "strings"
    "sync"
    "time"
)

var sqliteHealthV62 = struct {
    sync.RWMutex
    OpenedAt    string
    LastWriteAt string
    LastError   string
    LastErrorAt string
}{}

func resetSQLiteHealthV62() {
    sqliteHealthV62.Lock()
    sqliteHealthV62.OpenedAt = ""
    sqliteHealthV62.LastWriteAt = ""
    sqliteHealthV62.LastError = ""
    sqliteHealthV62.LastErrorAt = ""
    sqliteHealthV62.Unlock()
}

func recordSQLiteOpenV62(path string) {
    sqliteHealthV62.Lock()
    sqliteHealthV62.OpenedAt = nowV4()
    sqliteHealthV62.LastError = ""
    sqliteHealthV62.LastErrorAt = ""
    sqliteHealthV62.Unlock()
    _, _ = path, time.Now()
}

func recordSQLiteOpenErrorV62(err error) {
    if err == nil {
        return
    }
    sqliteHealthV62.Lock()
    sqliteHealthV62.LastError = err.Error()
    sqliteHealthV62.LastErrorAt = nowV4()
    sqliteHealthV62.Unlock()
}

func recordSQLiteWriteV62(err error) {
    if err == nil {
        sqliteHealthV62.Lock()
        sqliteHealthV62.LastWriteAt = nowV4()
        sqliteHealthV62.LastError = ""
        sqliteHealthV62.LastErrorAt = ""
        sqliteHealthV62.Unlock()
        return
    }
    recordSQLiteOpenErrorV62(err)
    _, _ = callHost("host.log", map[string]any{
        "level": "error",
        "message": "kcr sqlite write failed",
        "fields": map[string]any{"error": err.Error()},
    })
}

func resolvedSQLitePathV62(raw string) string {
    raw = strings.TrimSpace(raw)
    if raw == "" {
        raw = "key-chain-router.db"
    }
    if filepathIsAbsV62(raw) {
        return raw
    }
    runtimeState.RLock()
    pluginDir := runtimeState.pluginDir
    runtimeState.RUnlock()
    if pluginDir == "" {
        wd, _ := os.Getwd()
        pluginDir = wd
    }
    return joinPathV62(pluginDir, raw)
}

func filepathIsAbsV62(p string) bool {
    return len(p) > 0 && (p[0] == '/' || (len(p) > 2 && p[1] == ':'))
}

func joinPathV62(dir, name string) string {
    dir = strings.TrimRight(dir, "/\\")
    if dir == "" {
        return name
    }
    return dir + string(os.PathSeparator) + name
}

func sqliteStatusV62() map[string]any {
    v4Runtime.RLock()
    cfg := normalizeObservability(v4Runtime.state.Observability)
    sink := v4Runtime.sqlite
    v4Runtime.RUnlock()

    path := resolvedSQLitePathV62(cfg.SQLitePath)
    active := cfg.SQLiteEnabled && sink != nil && sink.db != nil
    if sink != nil && strings.TrimSpace(sink.path) != "" {
        path = sink.path
    }

    sqliteHealthV62.RLock()
    openedAt := sqliteHealthV62.OpenedAt
    lastWriteAt := sqliteHealthV62.LastWriteAt
    lastError := sqliteHealthV62.LastError
    lastErrorAt := sqliteHealthV62.LastErrorAt
    sqliteHealthV62.RUnlock()

    out := map[string]any{
        "enabled": cfg.SQLiteEnabled,
        "active": active,
        "path": path,
        "opened_at": openedAt,
        "last_write_at": lastWriteAt,
        "last_error": lastError,
        "last_error_at": lastErrorAt,
        "events": 0,
        "attempts": 0,
        "file_exists": false,
        "file_size": int64(0),
    }
    if st, err := os.Stat(path); err == nil {
        out["file_exists"] = true
        out["file_size"] = st.Size()
    }
    if !active {
        return out
    }

    var events, attempts int64
    if err := sink.db.QueryRow(`SELECT COUNT(*) FROM routing_events`).Scan(&events); err != nil {
        out["probe_error"] = err.Error()
    } else {
        out["events"] = events
    }
    if err := sink.db.QueryRow(`SELECT COUNT(*) FROM routing_attempts`).Scan(&attempts); err != nil {
        out["probe_error"] = err.Error()
    } else {
        out["attempts"] = attempts
    }
    var journal string
    if err := sink.db.QueryRow(`PRAGMA journal_mode`).Scan(&journal); err == nil {
        out["journal_mode"] = journal
    }
    return out
}

func queryEventsSQLiteV62(q url.Values) (map[string]any, bool) {
    v4Runtime.RLock()
    obs := normalizeObservability(v4Runtime.state.Observability)
    sink := v4Runtime.sqlite
    v4Runtime.RUnlock()
    if !obs.SQLiteEnabled || sink == nil || sink.db == nil {
        return nil, false
    }
    out, err := querySQLiteEventsV62(sink.db, q, obs)
    if err != nil {
        recordSQLiteOpenErrorV62(err)
        return nil, false
    }
    out["sqlite_status"] = sqliteStatusV62()
    return out, true
}

func querySQLiteEventsV62(db *sql.DB, q url.Values, obs ObservabilityConfig) (map[string]any, error) {
    where, args := sqliteWhereV62(q)
    limit := parseEventLimitV6(q.Get("limit"))

    statSQL := `SELECT COUNT(*),
        COALESCE(SUM(CASE WHEN decision=? THEN 1 ELSE 0 END),0),
        COALESCE(SUM(CASE WHEN decision=? THEN 1 ELSE 0 END),0),
        COALESCE(SUM(CASE WHEN decision=? THEN 1 ELSE 0 END),0),
        COALESCE(SUM(CASE WHEN success=1 THEN 1 ELSE 0 END),0),
        COALESCE(SUM(CASE WHEN success=0 THEN 1 ELSE 0 END),0),
        COALESCE(AVG(CASE WHEN duration_ms>=0 THEN duration_ms END),0),
        COALESCE(AVG((SELECT COUNT(*) FROM routing_attempts a WHERE a.trace_id=routing_events.trace_id)),0)
        FROM routing_events` + where
    statArgs := []any{decisionHandled, decisionFallbackToCPA, decisionBypass}
    statArgs = append(statArgs, args...)
    var total, handled, fallback, bypass, success, failed int
    var avgDuration, avgAttempts float64
    if err := db.QueryRow(statSQL, statArgs...).Scan(&total, &handled, &fallback, &bypass, &success, &failed, &avgDuration, &avgAttempts); err != nil {
        return nil, err
    }

    p95 := int64(0)
    durationWhere := where + ` AND COALESCE(duration_ms,-1)>=0`
    var durationCount int
    if err := db.QueryRow(`SELECT COUNT(*) FROM routing_events`+durationWhere, args...).Scan(&durationCount); err != nil {
        return nil, err
    }
    if durationCount > 0 {
        offset := (durationCount*95+99)/100 - 1
        p95Args := append([]any{}, args...)
        p95Args = append(p95Args, offset)
        if err := db.QueryRow(`SELECT duration_ms FROM routing_events`+durationWhere+` ORDER BY duration_ms LIMIT 1 OFFSET ?`, p95Args...).Scan(&p95); err != nil {
            return nil, err
        }
    }

    var windowTotal int
    if err := db.QueryRow(`SELECT COUNT(*) FROM routing_events WHERE COALESCE(reason,'') <> 'no_policy'`).Scan(&windowTotal); err != nil {
        return nil, err
    }

    queryArgs := append([]any{}, args...)
    queryArgs = append(queryArgs, limit)
    rows, err := db.Query(`SELECT
        COALESCE(trace_id,''), COALESCE(at,''), COALESCE(decision,''), COALESCE(reason,''),
        COALESCE(selection_reasons,''), COALESCE(policy_name,''), COALESCE(key_fingerprint,''), COALESCE(key_hint,''),
        COALESCE(rule_id,''), COALESCE(rule_name,''), COALESCE(strategy,''), COALESCE(model,''), COALESCE(stream,0),
        COALESCE(final_resource,''), COALESCE(provider,''), COALESCE(auth_index,''), COALESCE(status,0),
        COALESCE(duration_ms,0), COALESCE(success,0), COALESCE(error,'')
        FROM routing_events`+where+` ORDER BY at DESC LIMIT ?`, queryArgs...)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    events := make([]RoutingEvent, 0, limit)
    traceIDs := make([]string, 0, limit)
    index := map[string]int{}
    for rows.Next() {
        var ev RoutingEvent
        var reasonsJSON string
        var stream, ok int
        if err := rows.Scan(&ev.TraceID, &ev.At, &ev.Decision, &ev.Reason, &reasonsJSON,
            &ev.PolicyName, &ev.KeyFingerprint, &ev.KeyHint, &ev.RuleID, &ev.RuleName, &ev.Strategy,
            &ev.Model, &stream, &ev.Final, &ev.Provider, &ev.AuthIndex, &ev.Status, &ev.DurationMs, &ok, &ev.Error); err != nil {
            return nil, err
        }
        ev.Stream = stream != 0
        ev.Success = ok != 0
        if reasonsJSON != "" {
            _ = json.Unmarshal([]byte(reasonsJSON), &ev.SelectionReasons)
        }
        index[ev.TraceID] = len(events)
        traceIDs = append(traceIDs, ev.TraceID)
        events = append(events, ev)
    }
    if err := rows.Err(); err != nil {
        return nil, err
    }

    if len(traceIDs) > 0 {
        ph := strings.TrimSuffix(strings.Repeat("?,", len(traceIDs)), ",")
        attemptArgs := make([]any, len(traceIDs))
        for i := range traceIDs {
            attemptArgs[i] = traceIDs[i]
        }
        ar, err := db.Query(`SELECT COALESCE(trace_id,''), COALESCE(sequence,0), COALESCE(candidate,''),
            COALESCE(provider,''), COALESCE(auth_index,''), COALESCE(model,''), COALESCE(status,0),
            COALESCE(error,''), COALESCE(duration_ms,0)
            FROM routing_attempts WHERE trace_id IN (`+ph+`) ORDER BY sequence`, attemptArgs...)
        if err != nil {
            return nil, err
        }
        for ar.Next() {
            var trace string
            var seq int
            var a attemptResult
            if err := ar.Scan(&trace, &seq, &a.Candidate, &a.Provider, &a.AuthIndex, &a.Model, &a.Status, &a.Error, &a.DurationMs); err != nil {
                ar.Close()
                return nil, err
            }
            if i, ok := index[trace]; ok {
                events[i].Attempts = append(events[i].Attempts, a)
            }
        }
        if err := ar.Err(); err != nil {
            ar.Close()
            return nil, err
        }
        ar.Close()
    }

    facets, err := sqliteFacetsV62(db)
    if err != nil {
        return nil, err
    }
    successRate := float64(0)
    if total > 0 {
        successRate = float64(success) * 100 / float64(total)
    }
    return map[string]any{
        "ok": true,
        "source": "sqlite",
        "memory_limit": obs.MemoryLimit,
        "memory_on": obs.MemoryEnabled,
        "window_total": windowTotal,
        "matched": total,
        "returned": len(events),
        "stats": map[string]any{
            "total": total,
            "handled": handled,
            "fallback": fallback,
            "bypass": bypass,
            "success": success,
            "failed": failed,
            "success_rate": successRate,
            "avg_attempts": avgAttempts,
            "avg_duration_ms": int64(avgDuration + 0.5),
            "p95_duration_ms": p95,
        },
        "facets": facets,
        "events": events,
    }, nil
}

func sqliteWhereV62(q url.Values) (string, []any) {
    clauses := []string{`COALESCE(reason,'') <> 'no_policy'`}
    args := []any{}
    if cutoff := eventCutoffV6(strings.TrimSpace(q.Get("since"))); !cutoff.IsZero() {
        clauses = append(clauses, `at >= ?`)
        args = append(args, cutoff.Format(time.RFC3339Nano))
    }
    if v := strings.TrimSpace(q.Get("decision")); v != "" && v != "all" {
        clauses = append(clauses, `decision = ?`)
        args = append(args, v)
    }
    if v := strings.TrimSpace(q.Get("success")); v == "true" || v == "false" {
        clauses = append(clauses, `success = ?`)
        if v == "true" { args = append(args, 1) } else { args = append(args, 0) }
    }
    if v := strings.TrimSpace(q.Get("policy")); v != "" && v != "all" {
        clauses = append(clauses, `LOWER(COALESCE(policy_name,'')) = LOWER(?)`)
        args = append(args, v)
    }
    if v := strings.TrimSpace(q.Get("strategy")); v != "" && v != "all" {
        clauses = append(clauses, `LOWER(COALESCE(strategy,'')) = LOWER(?)`)
        args = append(args, v)
    }
    if v := strings.TrimSpace(q.Get("model")); v != "" && v != "all" {
        clauses = append(clauses, `LOWER(COALESCE(model,'')) = LOWER(?)`)
        args = append(args, v)
    }
    if v := strings.TrimSpace(q.Get("provider")); v != "" && v != "all" {
        clauses = append(clauses, `(LOWER(COALESCE(provider,'')) = LOWER(?) OR EXISTS (SELECT 1 FROM routing_attempts a WHERE a.trace_id=routing_events.trace_id AND LOWER(COALESCE(a.provider,'')) = LOWER(?)))`)
        args = append(args, v, v)
    }
    switch strings.TrimSpace(q.Get("status")) {
    case "2xx": clauses = append(clauses, `status >= 200 AND status < 300`)
    case "3xx": clauses = append(clauses, `status >= 300 AND status < 400`)
    case "4xx": clauses = append(clauses, `status >= 400 AND status < 500`)
    case "5xx": clauses = append(clauses, `status >= 500 AND status < 600`)
    case "error": clauses = append(clauses, `(success = 0 OR COALESCE(status,0) = 0)`)
    }
    if v := strings.ToLower(strings.TrimSpace(q.Get("q"))); v != "" {
        clauses = append(clauses, `(instr(lower(COALESCE(trace_id,'') || char(10) || COALESCE(decision,'') || char(10) || COALESCE(reason,'') || char(10) || COALESCE(selection_reasons,'') || char(10) || COALESCE(policy_name,'') || char(10) || COALESCE(key_hint,'') || char(10) || COALESCE(rule_id,'') || char(10) || COALESCE(rule_name,'') || char(10) || COALESCE(strategy,'') || char(10) || COALESCE(model,'') || char(10) || COALESCE(final_resource,'') || char(10) || COALESCE(provider,'') || char(10) || COALESCE(auth_index,'') || char(10) || COALESCE(error,'')), ?) > 0 OR EXISTS (SELECT 1 FROM routing_attempts a WHERE a.trace_id=routing_events.trace_id AND instr(lower(COALESCE(a.candidate,'') || char(10) || COALESCE(a.provider,'') || char(10) || COALESCE(a.auth_index,'') || char(10) || COALESCE(a.model,'') || char(10) || COALESCE(a.error,'')), ?) > 0))`)
        args = append(args, v, v)
    }
    return " WHERE " + strings.Join(clauses, " AND "), args
}

func sqliteFacetsV62(db *sql.DB) (map[string]any, error) {
    policies, err := sqliteFacetV62(db, `SELECT DISTINCT policy_name FROM routing_events WHERE COALESCE(reason,'') <> 'no_policy' AND COALESCE(policy_name,'') <> ''`)
    if err != nil { return nil, err }
    strategies, err := sqliteFacetV62(db, `SELECT DISTINCT strategy FROM routing_events WHERE COALESCE(reason,'') <> 'no_policy' AND COALESCE(strategy,'') <> ''`)
    if err != nil { return nil, err }
    models, err := sqliteFacetV62(db, `SELECT DISTINCT model FROM routing_events WHERE COALESCE(reason,'') <> 'no_policy' AND COALESCE(model,'') <> ''`)
    if err != nil { return nil, err }
    providers, err := sqliteFacetV62(db, `SELECT provider FROM routing_events WHERE COALESCE(reason,'') <> 'no_policy' AND COALESCE(provider,'') <> '' UNION SELECT provider FROM routing_attempts WHERE COALESCE(provider,'') <> ''`)
    if err != nil { return nil, err }
    return map[string]any{"policies": policies, "strategies": strategies, "providers": providers, "models": models}, nil
}

func sqliteFacetV62(db *sql.DB, query string) ([]string, error) {
    rows, err := db.Query(query)
    if err != nil { return nil, err }
    defer rows.Close()
    out := []string{}
    for rows.Next() {
        var s string
        if err := rows.Scan(&s); err != nil { return nil, err }
        s = strings.TrimSpace(s)
        if s != "" { out = append(out, s) }
    }
    if err := rows.Err(); err != nil { return nil, err }
    sort.Strings(out)
    return out, nil
}
'''

(ROOT / 'src/v062_sqlite_query.go').write_text(helper)

test = r'''package main

import (
    "net/url"
    "path/filepath"
    "testing"
    "time"
)

func TestSQLiteEventsRemainQueryableWithoutMemory(t *testing.T) {
    cfg := defaultObservability()
    cfg.SQLiteEnabled = true
    cfg.SQLitePath = filepath.Join(t.TempDir(), "routing.db")
    sink, err := newSQLiteSinkV4(cfg.SQLitePath, cfg)
    if err != nil { t.Fatal(err) }

    ev := RoutingEvent{
        TraceID: "persisted-trace",
        At: time.Now().UTC().Format(time.RFC3339Nano),
        Decision: decisionHandled,
        PolicyName: "persist-policy",
        RuleName: "persist-rule",
        Strategy: strategyOrdered,
        Model: "gpt-test",
        Final: "provider-a",
        Provider: "codex",
        Status: 200,
        DurationMs: 123,
        Success: true,
        Attempts: []attemptResult{{Candidate:"provider-a", Provider:"codex", AuthIndex:"idx-a", Model:"gpt-test", Status:200, DurationMs:120}},
        SelectionReasons: []string{"persisted reason"},
    }
    if err := sink.insert(ev); err != nil { sink.close(); t.Fatal(err) }

    v4Runtime.Lock()
    oldState, oldRecent, oldSink := v4Runtime.state, v4Runtime.recent, v4Runtime.sqlite
    v4Runtime.state.Observability = cfg
    v4Runtime.recent = nil
    v4Runtime.sqlite = sink
    v4Runtime.Unlock()
    defer func() {
        v4Runtime.Lock()
        v4Runtime.state, v4Runtime.recent, v4Runtime.sqlite = oldState, oldRecent, oldSink
        v4Runtime.Unlock()
        sink.close()
    }()

    got := queryEventsV4(url.Values{"limit": []string{"200"}})
    if got["source"] != "sqlite" { t.Fatalf("source=%v, want sqlite", got["source"]) }
    events, ok := got["events"].([]RoutingEvent)
    if !ok || len(events) != 1 { t.Fatalf("events=%#v", got["events"]) }
    if events[0].TraceID != "persisted-trace" || len(events[0].Attempts) != 1 {
        t.Fatalf("event=%+v", events[0])
    }
    if len(events[0].SelectionReasons) != 1 || events[0].SelectionReasons[0] != "persisted reason" {
        t.Fatalf("selection reasons=%v", events[0].SelectionReasons)
    }
    status := sqliteStatusV62()
    if status["active"] != true || status["events"].(int64) != 1 {
        t.Fatalf("sqlite status=%v", status)
    }
}
'''
(ROOT / 'src/v062_sqlite_test.go').write_text(test)

p = ROOT / 'src/v4_management.go'
s = p.read_text()
old = 'func queryEventsV4(q url.Values) map[string]any {\n\tv4Runtime.RLock()'
new = 'func queryEventsV4(q url.Values) map[string]any {\n\tif out, ok := queryEventsSQLiteV62(q); ok {\n\t\treturn out\n\t}\n\tv4Runtime.RLock()'
if old not in s: raise SystemExit('management queryEvents anchor missing')
s = s.replace(old, new, 1)
old = '\t\t"observability":   st.Observability,\n\t\t"recent_events":   recent,'
new = '\t\t"observability":   st.Observability,\n\t\t"sqlite_status":   sqliteStatusV62(),\n\t\t"recent_events":   recent,'
if old not in s: raise SystemExit('snapshot anchor missing')
s = s.replace(old, new, 1)
old = '\t\t"events": events,\n\t}\n}\n\nfunc parseEventLimitV6'
new = '\t\t"events": events,\n\t\t"sqlite_status": sqliteStatusV62(),\n\t}\n}\n\nfunc parseEventLimitV6'
if old not in s: raise SystemExit('memory events return anchor missing')
s = s.replace(old, new, 1)
p.write_text(s)

p = ROOT / 'src/v4_observability.go'
s = p.read_text()
old = '\tif !o.SQLiteEnabled {\n\t\treturn nil\n\t}'
new = '\tif !o.SQLiteEnabled {\n\t\tresetSQLiteHealthV62()\n\t\treturn nil\n\t}'
if old not in s: raise SystemExit('sqlite disabled anchor missing')
s = s.replace(old, new, 1)
old = '\tsink, err := newSQLiteSinkV4(path, o)\n\tif err != nil {\n\t\treturn err\n\t}\n\tv4Runtime.Lock()'
new = '\tsink, err := newSQLiteSinkV4(path, o)\n\tif err != nil {\n\t\trecordSQLiteOpenErrorV62(err)\n\t\treturn err\n\t}\n\trecordSQLiteOpenV62(path)\n\tv4Runtime.Lock()'
if old not in s: raise SystemExit('sqlite open anchor missing')
s = s.replace(old, new, 1)
s = s.replace('\t\tcase ev := <-s.ch:\n\t\t\t_ = s.insert(ev)', '\t\tcase ev := <-s.ch:\n\t\t\trecordSQLiteWriteV62(s.insert(ev))', 1)
s = s.replace('\t\t\t\tcase ev := <-s.ch:\n\t\t\t\t\t_ = s.insert(ev)', '\t\t\t\tcase ev := <-s.ch:\n\t\t\t\t\trecordSQLiteWriteV62(s.insert(ev))', 1)
old = '\t\tduration_ms INTEGER,\n\t\tsuccess INTEGER\n\t);'
new = '\t\tduration_ms INTEGER,\n\t\tsuccess INTEGER,\n\t\terror TEXT\n\t);'
if old not in s: raise SystemExit('schema error-column anchor missing')
s = s.replace(old, new, 1)
old = '\t_, _ = db.Exec(`ALTER TABLE routing_events ADD COLUMN selection_reasons TEXT`)'
new = '\t_, _ = db.Exec(`ALTER TABLE routing_events ADD COLUMN selection_reasons TEXT`)\n\t_, _ = db.Exec(`ALTER TABLE routing_events ADD COLUMN error TEXT`)'
if old not in s: raise SystemExit('alter anchor missing')
s = s.replace(old, new, 1)
old = '`INSERT OR REPLACE INTO routing_events(trace_id,at,decision,reason,selection_reasons,policy_name,key_fingerprint,key_hint,rule_id,rule_name,strategy,model,stream,final_resource,provider,auth_index,status,duration_ms,success) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,\n\t\tev.TraceID, ev.At, ev.Decision, ev.Reason, string(reasonsJSON), ev.PolicyName, ev.KeyFingerprint, ev.KeyHint, ev.RuleID, ev.RuleName, ev.Strategy, ev.Model, boolIntV4(ev.Stream), ev.Final, ev.Provider, ev.AuthIndex, ev.Status, ev.DurationMs, boolIntV4(ev.Success),'
new = '`INSERT OR REPLACE INTO routing_events(trace_id,at,decision,reason,selection_reasons,policy_name,key_fingerprint,key_hint,rule_id,rule_name,strategy,model,stream,final_resource,provider,auth_index,status,duration_ms,success,error) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,\n\t\tev.TraceID, ev.At, ev.Decision, ev.Reason, string(reasonsJSON), ev.PolicyName, ev.KeyFingerprint, ev.KeyHint, ev.RuleID, ev.RuleName, ev.Strategy, ev.Model, boolIntV4(ev.Stream), ev.Final, ev.Provider, ev.AuthIndex, ev.Status, ev.DurationMs, boolIntV4(ev.Success), ev.Error,'
if old not in s: raise SystemExit('insert anchor missing')
s = s.replace(old, new, 1)
p.write_text(s)

p = ROOT / 'src/ui.js'
s = p.read_text()
old = "  $('oHeaders').value = String(!!o.response_headers);\n}"
new = r'''  $('oHeaders').value = String(!!o.response_headers);
  const h = SNAP.sqlite_status || {};
  const health = $('sqliteHealth');
  if (health) {
    if (!o.sqlite_enabled) {
      health.innerHTML = '<div class="muted small">SQLite 当前未开启。</div>';
    } else {
      const cls = h.active ? 'oknote' : 'note';
      const size = Number(h.file_size || 0);
      const sizeText = size >= 1048576 ? (size / 1048576).toFixed(2) + ' MB' : size >= 1024 ? (size / 1024).toFixed(1) + ' KB' : size + ' B';
      const bits = [
        '数据库：' + (h.path || o.sqlite_path || '-'),
        '文件：' + (h.file_exists ? sizeText : '尚未创建'),
        'Events：' + (h.events || 0),
        'Attempts：' + (h.attempts || 0),
        h.journal_mode ? 'Journal：' + h.journal_mode : '',
        h.last_write_at ? '最后写入：' + h.last_write_at : '最后写入：暂无'
      ].filter(Boolean);
      health.innerHTML = '<div class="' + cls + '"><b>SQLite ' + (h.active ? '运行中' : '未运行') + '</b><div class="small" style="margin-top:5px;word-break:break-all">' + esc(bits.join(' · ')) + '</div>' +
        (h.last_error ? '<div class="red small" style="margin-top:5px">最后错误：' + esc(h.last_error) + (h.last_error_at ? ' · ' + esc(h.last_error_at) : '') + '</div>' : '') +
        (h.probe_error ? '<div class="red small" style="margin-top:5px">健康检查：' + esc(h.probe_error) + '</div>' : '') + '</div>';
    }
  }
}'''
if old not in s: raise SystemExit('ui renderObs anchor missing')
s = s.replace(old, new, 1)
old = "  const meta = '当前内存窗口 ' + (EVENT_DATA.window_total || 0) + ' 条 · 匹配 ' + (EVENT_DATA.matched || 0) +\n    ' 条 · 返回 ' + (EVENT_DATA.returned || 0) + ' 条 · 内存上限 ' + (EVENT_DATA.memory_limit || 0) +\n    (EVENT_DATA.memory_on ? '' : '（内存记录已关闭）');\n  $('eventResultMeta').textContent = meta;"
new = r'''  const source = EVENT_DATA.source === 'sqlite' ? 'SQLite' : 'Memory';
  const health = EVENT_DATA.sqlite_status || {};
  const window = source === 'SQLite' ? '数据库记录 ' : '当前内存窗口 ';
  let meta = '数据源 ' + source + ' · ' + window + (EVENT_DATA.window_total || 0) + ' 条 · 匹配 ' + (EVENT_DATA.matched || 0) +
    ' 条 · 返回 ' + (EVENT_DATA.returned || 0) + ' 条';
  if (source === 'Memory') meta += ' · 内存上限 ' + (EVENT_DATA.memory_limit || 0) + (EVENT_DATA.memory_on ? '' : '（内存记录已关闭）');
  if (health.last_error) meta += ' · SQLite 异常：' + health.last_error;
  $('eventResultMeta').textContent = meta;'''
if old not in s: raise SystemExit('ui event meta anchor missing')
s = s.replace(old, new, 1)
p.write_text(s)

p = ROOT / 'src/page.html'
s = p.read_text()
old = 'v0.6 起，未配置 KCR Policy 的 API Key 请求不进入内存、SQLite 或 KCR routing log。</div><div class="row" style="margin-top:16px">'
new = 'v0.6 起，未配置 KCR Policy 的 API Key 请求不进入内存、SQLite 或 KCR routing log。</div><div id="sqliteHealth" style="margin-top:12px"></div><div class="row" style="margin-top:16px">'
if old not in s: raise SystemExit('page sqlite health anchor missing')
s = s.replace(old, new, 1)
p.write_text(s)

print('v0.6.2 patch applied')
