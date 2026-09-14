package main

import "testing"

func TestV10CrossPrefixFailoverAcceptsRegisteredPrefixedAlias(t *testing.T) {
	config := `codex-api-key:
  - api-key: sk-foo
    prefix: foo
    base-url: https://foo.example/v1
    models:
      - name: gpt-5.6-luna
        alias: foo/gpt-5.6-luna
  - api-key: sk-bar
    prefix: bar
    base-url: https://bar.example/v1
    models:
      - name: gpt-5.6-luna
        alias: bar/gpt-5.6-luna
`
	withCPAConfigPathForTest(t, config)
	_, resources := parseCPAConfig(config)
	if len(resources) != 2 {
		t.Fatalf("resources=%#v", resources)
	}
	bar := resources[1]
	candidate := &PolicyCandidate{ID: "bar", ResourceID: bar.ID, ResourceKind: "API", Provider: bar.Provider, AuthIndex: bar.AuthIndex, Enabled: true}
	got, prefix := candidateScopedModelV10(candidate, "foo/gpt-5.6-luna")
	if prefix != "bar" || got != "bar/gpt-5.6-luna" {
		t.Fatalf("scope=(%q,%q)", got, prefix)
	}
}
