package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestScopeCandidateModelsV9UsesConfigPrefixAndReconcilesRotatedKeyInSameSlot(t *testing.T) {
	config := `openai-compatibility:
  - name: loveapi
    prefix: loveapi-luna
    base-url: https://love.example/v1
    api-key-entries:
      - api-key: sk-new
        proxy-url: http://proxy.local:8080
    models:
      - name: gpt-5.6-luna
        alias: gpt-5.6-luna
`
	withCPAConfigPathForTest(t, config)
	_, resources := parseCPAConfig(config)
	if len(resources) != 1 {
		t.Fatalf("resources=%#v", resources)
	}
	current := resources[0]
	slot, ok := parseAPIResourceSlotV9(current.ID)
	if !ok {
		t.Fatalf("current resource id is not slot-addressable: %q", current.ID)
	}

	candidate := &PolicyCandidate{
		ID:           "loveapi",
		Name:         "loveapi-luna",
		ResourceID:   fmt.Sprintf("api:%s:%s:%d:%d", current.Provider, "stale-auth-index", slot.Outer, slot.Inner),
		ResourceKind: "API",
		Provider:     current.Provider,
		AuthIndex:    "4a74566302532301",
		Enabled:      true,
	}

	scopeCandidateModelsV9([]*PolicyCandidate{candidate}, "gpt-5.6-luna")
	if candidate.AuthIndex != current.AuthIndex {
		t.Fatalf("AuthIndex=%q, want current %q", candidate.AuthIndex, current.AuthIndex)
	}
	if candidate.ResourceID != current.ID {
		t.Fatalf("ResourceID=%q, want current %q", candidate.ResourceID, current.ID)
	}
	if candidate.OverrideModel != "loveapi-luna/gpt-5.6-luna" {
		t.Fatalf("OverrideModel=%q", candidate.OverrideModel)
	}
}

func TestCurrentConfigResourceForCandidateV9DoesNotCrossProviderOrSlot(t *testing.T) {
	config := `openai-compatibility:
  - name: loveapi
    prefix: loveapi-luna
    base-url: https://love.example/v1
    api-key-entries:
      - api-key: sk-one
      - api-key: sk-two
    models:
      - name: gpt-5.6-luna
        alias: gpt-5.6-luna
`
	withCPAConfigPathForTest(t, config)
	_, resources := parseCPAConfig(config)
	if len(resources) != 2 {
		t.Fatalf("resources=%#v", resources)
	}
	slot, ok := parseAPIResourceSlotV9(resources[0].ID)
	if !ok {
		t.Fatal("slot parse failed")
	}

	wrongProvider := &PolicyCandidate{
		ResourceID:   fmt.Sprintf("api:%s:%s:%d:%d", slot.Provider, "old", slot.Outer, slot.Inner),
		ResourceKind: "API",
		Provider:     "openai-compatible-other",
		AuthIndex:    "stale",
	}
	if got := currentConfigResourceForCandidateV9(wrongProvider, resources); got != nil {
		t.Fatalf("provider-crossing reconciliation returned %#v", got)
	}

	wrongSlot := &PolicyCandidate{
		ResourceID:   fmt.Sprintf("api:%s:%s:%d:%d", slot.Provider, "old", slot.Outer, 99),
		ResourceKind: "API",
		Provider:     slot.Provider,
		AuthIndex:    "stale",
	}
	if got := currentConfigResourceForCandidateV9(wrongSlot, resources); got != nil {
		t.Fatalf("slot-crossing reconciliation returned %#v", got)
	}
}

func TestScopeCandidateModelsV9DoesNotDoublePrefix(t *testing.T) {
	config := `codex-api-key:
  - api-key: sk-codex
    prefix: plus
    base-url: https://codex.example/v1
    models:
      - name: gpt-5.6-luna
        alias: gpt-5.6-luna
`
	withCPAConfigPathForTest(t, config)
	_, resources := parseCPAConfig(config)
	if len(resources) != 1 {
		t.Fatalf("resources=%#v", resources)
	}
	r := resources[0]
	candidate := &PolicyCandidate{ResourceID: r.ID, ResourceKind: "API", Provider: r.Provider, AuthIndex: r.AuthIndex, OverrideModel: "plus/gpt-5.6-luna", Enabled: true}
	scopeCandidateModelsV9([]*PolicyCandidate{candidate}, "gpt-5.6-luna")
	if strings.Count(candidate.OverrideModel, "plus/") != 1 || candidate.OverrideModel != "plus/gpt-5.6-luna" {
		t.Fatalf("OverrideModel=%q", candidate.OverrideModel)
	}
}

func TestScopeCandidateModelsV9DoesNotInventPrefixForUnregisteredConfigModel(t *testing.T) {
	config := `codex-api-key:
  - api-key: sk-codex
    prefix: plus
    base-url: https://codex.example/v1
    models:
      - name: gpt-5.6-luna
        alias: luna
`
	withCPAConfigPathForTest(t, config)
	_, resources := parseCPAConfig(config)
	if len(resources) != 1 {
		t.Fatalf("resources=%#v", resources)
	}
	r := resources[0]
	candidate := &PolicyCandidate{ResourceID: r.ID, ResourceKind: "API", Provider: r.Provider, AuthIndex: r.AuthIndex, Enabled: true}
	scopeCandidateModelsV9([]*PolicyCandidate{candidate}, "gpt-anything")
	if candidate.OverrideModel != "" {
		t.Fatalf("unregistered model was incorrectly scoped: %q", candidate.OverrideModel)
	}
}
