package main

import (
	"os"
	"strings"
	"testing"
)

func TestPatchTopListItemPriorityV11InsertsWithoutReformattingConfig(t *testing.T) {
	raw := "# keep me\ncodex-api-key:\n  - api-key: sk-one # inline\n    base-url: https://one.example/v1\n  - api-key: sk-two\n    priority: 3\n    base-url: https://two.example/v1\napi-keys:\n  - downstream\n"
	patched, changed, err := patchTopListItemPriorityV11(raw, configPriorityLocationV11{Section: "codex-api-key", Outer: 0}, 110)
	if err != nil { t.Fatal(err) }
	if !changed { t.Fatal("expected config change") }
	if !strings.Contains(patched, "  - api-key: sk-one # inline\n    priority: 110\n    base-url: https://one.example/v1") {
		t.Fatalf("unexpected patch:\n%s", patched)
	}
	if !strings.Contains(patched, "# keep me") || !strings.Contains(patched, "priority: 3") {
		t.Fatalf("unrelated config was reformatted/lost:\n%s", patched)
	}
}

func TestPatchTopListItemPriorityV11ReplacesExistingValue(t *testing.T) {
	raw := "codex-api-key:\n  - api-key: sk-one\n    priority: 7\n    base-url: https://one.example/v1\n"
	patched, changed, err := patchTopListItemPriorityV11(raw, configPriorityLocationV11{Section: "codex-api-key", Outer: 0}, 110)
	if err != nil { t.Fatal(err) }
	if !changed || strings.Contains(patched, "priority: 7") || !strings.Contains(patched, "priority: 110") {
		t.Fatalf("unexpected patch:\n%s", patched)
	}
}

func TestConfigPriorityLocationV11OpenAICompatibilityUsesProviderOuterPriority(t *testing.T) {
	config := `openai-compatibility:
  - name: loveapi
    priority: 9
    base-url: https://love.example/v1
    api-key-entries:
      - api-key: sk-one
      - api-key: sk-two
    models:
      - name: gpt-5.6-luna
        alias: gpt-5.6-luna
`
	_, resources := parseCPAConfig(config)
	if len(resources) != 2 { t.Fatalf("resources=%#v", resources) }
	c := &PolicyCandidate{Name: "love-two", Provider: resources[1].Provider, AuthIndex: resources[1].AuthIndex, ResourceKind: "API"}
	loc, err := configPriorityLocationV11ForCandidate(config, c)
	if err != nil { t.Fatal(err) }
	if loc.Section != "openai-compatibility" || loc.Outer != 0 {
		t.Fatalf("location=%+v", loc)
	}
	patched, changed, err := patchTopListItemPriorityV11(config, loc, 110)
	if err != nil { t.Fatal(err) }
	if !changed || strings.Count(patched, "priority: 110") != 1 {
		t.Fatalf("provider priority not updated exactly once:\n%s", patched)
	}
}

func TestPersistConfigPriorityV11ChangesPriorityWithoutChangingExactAuthID(t *testing.T) {
	config := `codex-api-key:
  - api-key: sk-low
    priority: 1
    base-url: https://low.example/v1
    models:
      - name: gpt-5.6-luna
        alias: gpt-5.6-luna
`
	path := withCPAConfigPathForTest(t, config)
	_, resources := parseCPAConfig(config)
	if len(resources) != 1 { t.Fatalf("resources=%#v", resources) }
	r := resources[0]
	beforeID := stableID("codex:apikey", "sk-low", "https://low.example/v1", "", "", "")
	c := &PolicyCandidate{Name: "low", ResourceID: r.ID, ResourceKind: "API", Provider: r.Provider, AuthIndex: r.AuthIndex}
	if err := persistConfigPriorityV11(c, 110); err != nil { t.Fatal(err) }
	after, err := os.ReadFile(path)
	if err != nil { t.Fatal(err) }
	if !strings.Contains(string(after), "priority: 110") { t.Fatalf("config=%s", after) }
	priority, ok := configPriorityForCandidateV10(c)
	if !ok || priority != 110 { t.Fatalf("priority=(%d,%v)", priority, ok) }
	afterID := stableID("codex:apikey", "sk-low", "https://low.example/v1", "", "", "")
	if beforeID != afterID { t.Fatalf("priority update changed exact AuthID: %q -> %q", beforeID, afterID) }
}

func TestEnsureCandidateCPAPriorityV11RaisesLowerConfigCredentialToProviderMax(t *testing.T) {
	config := `codex-api-key:
  - api-key: sk-high
    priority: 110
    base-url: https://high.example/v1
    models:
      - name: gpt-5.6-luna
        alias: gpt-5.6-luna
  - api-key: sk-low
    priority: 0
    base-url: https://low.example/v1
    models:
      - name: gpt-5.6-luna
        alias: gpt-5.6-luna
`
	withCPAConfigPathForTest(t, config)
	_, parsed := parseCPAConfig(config)
	if len(parsed) != 2 { t.Fatalf("resources=%#v", parsed) }
	low := parsed[1]
	c := &PolicyCandidate{Name: "low", ResourceID: low.ID, ResourceKind: "API", Provider: low.Provider, AuthIndex: low.AuthIndex, Enabled: true}
	changed, err := ensureCandidateCPAPriorityV11(c)
	if err != nil { t.Fatal(err) }
	if !changed { t.Fatal("expected managed priority update") }
	priority, ok := configPriorityForCandidateV10(c)
	if !ok || priority != 110 { t.Fatalf("priority=(%d,%v), want 110", priority, ok) }
	changed, err = ensureCandidateCPAPriorityV11(c)
	if err != nil { t.Fatal(err) }
	if changed { t.Fatal("managed priority update must be idempotent") }
}

func TestManagedPriorityControlFailureDoesNotPoisonCandidateHealth(t *testing.T) {
	err := &managedPriorityTestError{}
	if !isRoutingControlFailureV8(err) {
		t.Fatal("managed priority failures must be routing-control failures")
	}
}

func TestCandidateNotEligibleV11OnlyRetriesProvenLocalPrefilterFailure(t *testing.T) {
	if !isCandidateNotEligibleV11(&managedPriorityTextError{"HOST: KCR pinned credential is not eligible in the current CPA candidate set"}) {
		t.Fatal("expected exact pre-scheduler eligibility failure to be retryable")
	}
	if isCandidateNotEligibleV11(errSchedulerTicketUnclaimed) {
		t.Fatal("scheduler ownership loss may have reached another scheduler and must not be retried")
	}
}

type managedPriorityTestError struct{}
func (*managedPriorityTestError) Error() string { return "kcr managed priority: test failure" }

type managedPriorityTextError struct{ text string }
func (e *managedPriorityTextError) Error() string { return e.text }
