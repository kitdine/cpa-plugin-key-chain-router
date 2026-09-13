package main

import "testing"

func TestConfigPriorityForCandidateV10SimpleProvider(t *testing.T) {
	config := `codex-api-key:
  - api-key: sk-plus
    base-url: https://plus.example/v1
    prefix: plus
    priority: 110
    models:
      - name: gpt-5.6-luna
        alias: gpt-5.6-luna
`
	withCPAConfigPathForTest(t, config)
	_, resources := parseCPAConfig(config)
	if len(resources) != 1 {
		t.Fatalf("resources=%#v", resources)
	}
	c := &PolicyCandidate{Provider: resources[0].Provider, AuthIndex: resources[0].AuthIndex}
	priority, ok := configPriorityForCandidateV10(c)
	if !ok || priority != 110 {
		t.Fatalf("priority=%d ok=%v", priority, ok)
	}
}

func TestConfigPriorityForCandidateV10OpenAICompatibility(t *testing.T) {
	config := `openai-compatibility:
  - name: loveapi
    base-url: https://love.example/v1
    priority: 42
    api-key-entries:
      - api-key: sk-love
`
	withCPAConfigPathForTest(t, config)
	_, resources := parseCPAConfig(config)
	if len(resources) != 1 {
		t.Fatalf("resources=%#v", resources)
	}
	c := &PolicyCandidate{Provider: resources[0].Provider, AuthIndex: resources[0].AuthIndex}
	priority, ok := configPriorityForCandidateV10(c)
	if !ok || priority != 42 {
		t.Fatalf("priority=%d ok=%v", priority, ok)
	}
}

func TestConfigPriorityForCandidateV10IsProviderScoped(t *testing.T) {
	config := `codex-api-key:
  - api-key: sk-codex
    base-url: https://same.example/v1
    priority: 7
xai-api-key:
  - api-key: sk-xai
    base-url: https://same.example/v1
    priority: 3
`
	withCPAConfigPathForTest(t, config)
	_, resources := parseCPAConfig(config)
	if len(resources) != 2 {
		t.Fatalf("resources=%#v", resources)
	}
	for _, r := range resources {
		priority, ok := configPriorityForCandidateV10(&PolicyCandidate{Provider: r.Provider, AuthIndex: r.AuthIndex})
		if !ok {
			t.Fatalf("priority not found for %#v", r)
		}
		if r.Provider == "codex" && priority != 7 {
			t.Fatalf("codex priority=%d", priority)
		}
		if r.Provider == "xai" && priority != 3 {
			t.Fatalf("xai priority=%d", priority)
		}
	}
}
