package main

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
		map[string]any{"id": "runtime-auth-a", "provider": "codex"},
	}
	if !schedulerCandidateEligible(candidates, "runtime-auth-a", "codex") {
		t.Fatal("expected matching runtime AuthID scheduler candidate to be eligible")
	}
	if schedulerCandidateEligible(candidates, "runtime-auth-b", "codex") {
		t.Fatal("runtime AuthID absent from Candidates must be rejected")
	}
	if schedulerCandidateEligible(candidates, "runtime-auth-a", "claude") {
		t.Fatal("provider-mismatched scheduler candidate must be rejected")
	}
	if schedulerCandidateEligible(nil, "runtime-auth-a", "codex") {
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
		Candidates: []*PolicyCandidate{
			{ID: "c1", Name: "A", AuthIndex: "idx", Provider: "codex", Priority: 100, Weight: 3, Enabled: true},
			{ID: "c2", Name: "B", AuthIndex: "idx-b", Provider: "codex", Priority: 100, Weight: 1, Enabled: true},
		},
		Failover: FailoverPolicy{RateLimit: failNext},
	}
	ev := RoutingEvent{
		RuleID: "r1", RuleName: "rule", Strategy: strategyPriorityWeighted,
		ruleSnapshot: cloneRuleV4(original),
		Attempts:     []attemptResult{{Candidate: "A", AuthIndex: "idx", Provider: "codex", Status: 200}},
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
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE routing_events (trace_id TEXT PRIMARY KEY, at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := ensureRoutingEventColumnV4(db, "selection_reasons", "TEXT"); err != nil {
		t.Fatal(err)
	}
	if err := ensureRoutingEventColumnV4(db, "selection_reasons", "TEXT"); err != nil {
		t.Fatalf("second migration should be a no-op: %v", err)
	}
	if err := ensureRoutingEventColumnV4(db, "bogus", "TEXT"); err == nil {
		t.Fatal("unexpected column must be rejected")
	}
}

func TestSQLiteWriteFailureDisablesSQLiteQuerySource(t *testing.T) {
	resetSQLiteHealthV62()
	recordSQLiteOpenV62("test.db")
	if !sqliteWriterHealthyV62() {
		t.Fatal("fresh sqlite sink should be healthy")
	}
	recordSQLiteWriteV62(assertErr("disk full"))
	if sqliteWriterHealthyV62() {
		t.Fatal("write failure must degrade sqlite source")
	}
	recordSQLiteWriteV62(nil)
	if sqliteWriterHealthyV62() {
		t.Fatal("degraded state must remain sticky until sink restart")
	}
}

type assertErr string

func (e assertErr) Error() string { return string(e) }

func TestSQLiteEventQueryUsesConsistentTransaction(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
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
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO routing_events(trace_id,at,decision,reason,success,duration_ms) VALUES(?,?,?,?,?,?)`, "t1", time.Now().UTC().Format(time.RFC3339Nano), decisionHandled, "", 1, 12); err != nil {
		t.Fatal(err)
	}
	out, err := querySQLiteEventsV62(db, url.Values{"limit": {"10"}}, ObservabilityConfig{MemoryEnabled: true, MemoryLimit: 500})
	if err != nil {
		t.Fatal(err)
	}
	if out["matched"].(int) != out["returned"].(int) {
		t.Fatalf("snapshot response mismatch: matched=%v returned=%v", out["matched"], out["returned"])
	}
}
