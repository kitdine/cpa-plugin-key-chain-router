package main

import (
	"strings"
	"testing"
)

func TestV10PrefixScopeUsesExactCredentialAndPreservesHealthIdentity(t *testing.T) {
	config := `codex-api-key:
  - api-key: sk-plus
    prefix: plus
    base-url: https://plus.example/v1
    models:
      - name: gpt-5.6-luna
        alias: gpt-5.6-luna
  - api-key: sk-four
    prefix: four
    base-url: https://four.example/v1
    models:
      - name: gpt-5.6-luna
        alias: gpt-5.6-luna
`
	withCPAConfigPathForTest(t, config)
	_, resources := parseCPAConfig(config)
	if len(resources) != 2 {
		t.Fatalf("resources=%#v", resources)
	}
	plus := resources[0]
	persisted := &PolicyCandidate{ID: "plus", Name: "plus", ResourceID: plus.ID, ResourceKind: "API", Provider: plus.Provider, AuthIndex: plus.AuthIndex, Enabled: true, Priority: 100, Weight: 1}
	requestClone := *persisted
	ranked := scopeCandidateModelsV9([]*PolicyCandidate{&requestClone}, "gpt-5.6-luna")
	if got := ranked[0].OverrideModel; got != "plus/gpt-5.6-luna" {
		t.Fatalf("scoped model=%q", got)
	}
	if !ranked[0].executionScoped || ranked[0].executionOriginalOverride != "" {
		t.Fatalf("request identity markers=%+v", ranked[0])
	}
	if !candidateHealthConfigEqualV4(persisted, ranked[0]) {
		t.Fatal("request-local model scoping must not invalidate candidate health identity")
	}
}

func TestV10CrossPrefixFailoverReplacesExistingPrefix(t *testing.T) {
	config := `codex-api-key:
  - api-key: sk-plus
    prefix: plus
    base-url: https://plus.example/v1
    models:
      - name: gpt-5.6-luna
        alias: gpt-5.6-luna
  - api-key: sk-four
    prefix: four
    base-url: https://four.example/v1
    models:
      - name: gpt-5.6-luna
        alias: gpt-5.6-luna
`
	withCPAConfigPathForTest(t, config)
	_, resources := parseCPAConfig(config)
	four := resources[1]
	candidate := &PolicyCandidate{ID: "four", ResourceID: four.ID, ResourceKind: "API", Provider: four.Provider, AuthIndex: four.AuthIndex, Enabled: true}
	got, prefix := candidateScopedModelV10(candidate, "plus/gpt-5.6-luna")
	if prefix != "four" || got != "four/gpt-5.6-luna" {
		t.Fatalf("scope=(%q,%q)", got, prefix)
	}
	if strings.Contains(got, "four/plus/") {
		t.Fatalf("double credential prefix: %q", got)
	}
}

func TestV10DoesNotInventPrefixForUnregisteredModel(t *testing.T) {
	config := `codex-api-key:
  - api-key: sk-plus
    prefix: plus
    base-url: https://plus.example/v1
    models:
      - name: gpt-5.6-luna
        alias: gpt-5.6-luna
`
	withCPAConfigPathForTest(t, config)
	_, resources := parseCPAConfig(config)
	candidate := &PolicyCandidate{ID: "plus", ResourceID: resources[0].ID, ResourceKind: "API", Provider: resources[0].Provider, AuthIndex: resources[0].AuthIndex, Enabled: true}
	got, prefix := candidateScopedModelV10(candidate, "not-registered")
	if prefix != "" || got != "not-registered" {
		t.Fatalf("unregistered model scope=(%q,%q)", got, prefix)
	}
}

func TestV10OpenAICompatibleExactHistoricalIdentity(t *testing.T) {
	config := `openai-compatibility:
  - name: loveapi
    prefix: loveapi-luna
    base-url: https://love.example/v1
    api-key-entries:
      - api-key: sk-love
        proxy-url: http://proxy.example:8080
    models:
      - name: gpt-5.6-luna
        alias: gpt-5.6-luna
`
	withCPAConfigPathForTest(t, config)
	stale := stableAuthIndex("openai-compatibility:https://love.example/v1+sk-love")
	got, err := resolveLegacySyntheticAuthIDV10(stale, "openai-compatible-loveapi")
	if err != nil {
		t.Fatal(err)
	}
	want := stableID("openai-compatibility:loveapi", "sk-love", "https://love.example/v1", "http://proxy.example:8080")
	if got != want {
		t.Fatalf("live id=%q want %q", got, want)
	}
}

func TestV10ExactIdentityNeverMigratesByConfigSlot(t *testing.T) {
	config := `openai-compatibility:
  - name: loveapi
    base-url: https://love.example/v1
    api-key-entries:
      - api-key: sk-new-first
      - api-key: sk-original-moved
`
	withCPAConfigPathForTest(t, config)
	old := stableAuthIndex("openai-compatibility:https://love.example/v1+sk-old-removed")
	if got, err := resolveLegacySyntheticAuthIDV10(old, "openai-compatible-loveapi"); err != nil || got != "" {
		t.Fatalf("removed identity must fail closed, got=%q err=%v", got, err)
	}
}
