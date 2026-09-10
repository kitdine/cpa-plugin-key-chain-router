package main

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
