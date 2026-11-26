package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCPAConfigV8InventoryAndExactBinding(t *testing.T) {
	const raw = `
config-version: 8
access:
  api-keys: [test-downstream-client-secret]
api-keys:
  codex:
    - name: group
      base-url: https://codex.example/v1
      prefix: /team/
      proxy-url: http://proxy.example:8080
      priority: 5
      headers: {X-A: "a,b:c", X-B: "x:y,z"}
      models: [{name: upstream-model, alias: alias-one}]
      keys:
        - api-key: sk-test-codex-first-secret
        - api-key: sk-test-codex-second-secret
          priority: 0
          proxy-url: ""
          headers: {}
          models: []
  openai-compatibility:
    - name: Compatible
      base-url: https://compatible.example/v1
      prefix: service
      priority: 7
      models: [{name: upstream-model, alias: custom}]
      keys: [{api-key: sk-test-compat-secret, proxy-url: "http://proxy.example:8080"}]
    - name: Disabled
      disabled: true
      base-url: https://disabled.example/v1
      keys: [{api-key: sk-test-disabled-secret}]
    - name: Keyless
      base-url: https://keyless.example/v1
      keys: []
  gemini: [{keys: [{api-key: sk-test-gemini-secret}]}]
  interactions: [{keys: [{api-key: sk-test-interactions-secret}]}]
  claude: [{keys: [{api-key: sk-test-claude-secret}]}]
  vertex: [{base-url: https://vertex.example, keys: [{api-key: sk-test-vertex-secret}]}]
  xai: [{base-url: https://xai.example, keys: [{api-key: sk-test-xai-secret}]}]
  meta: [{keys: [{api-key: sk-test-meta-secret}]}]
codex-api-key:
  - api-key: sk-stale-key
    base-url: https://ignored.example/v1
`
	withCPAConfigPathForTest(t, raw)
	keys, resources, errText := currentEnvironment()
	if errText != "" || len(keys) != 1 || keys[0].Fingerprint != fingerprint("test-downstream-client-secret") {
		t.Fatalf("downstream keys mismatch: %d, %q", len(keys), errText)
	}
	if len(resources) != 10 {
		t.Fatalf("want ten active API credentials, got %d: %#v", len(resources), resources)
	}
	for _, r := range resources {
		if r.Kind != "API" {
			t.Fatal("config credential must be classified as API")
		}
	}
	payload, _ := json.Marshal(resources)
	if strings.Contains(string(payload), "-secret") || strings.Contains(string(payload), "disabled.example") || strings.Contains(string(payload), "ignored.example") {
		t.Fatal("snapshot exposed raw credentials or inactive/unselected groups")
	}

	cases := []struct {
		provider, index, id, suggested string
		priority                       int
	}{
		{"codex", stableAuthIndex("codex-api-key:https://codex.example/v1+sk-test-codex-first-secret"),
			stableID("codex:apikey", "sk-test-codex-first-secret", "https://codex.example/v1", "http://proxy.example:8080", "team", "X-A\x00a,b:c\x00X-B\x00x:y,z\x00"),
			"team/alias-one", 5},
		{"codex", stableAuthIndex("codex-api-key:https://codex.example/v1+sk-test-codex-second-secret"),
			stableID("codex:apikey", "sk-test-codex-second-secret", "https://codex.example/v1", "", "team", ""),
			"", 0},
		{"openai-compatible-compatible", stableAuthIndex("openai-compatibility:https://compatible.example/v1+sk-test-compat-secret"),
			stableID("openai-compatibility:compatible", "sk-test-compat-secret", "https://compatible.example/v1", "http://proxy.example:8080"),
			"service/custom", 7},
		{"openai-compatible-keyless", stableAuthIndex("id:" + stableID("openai-compatibility:keyless", "https://keyless.example/v1")),
			stableID("openai-compatibility:keyless", "https://keyless.example/v1"), "", 0},
	}
	for _, tc := range cases {
		var got *apiResource
		for _, r := range resourcesWithExactIDsV10() {
			if r.Provider == tc.provider && r.AuthIndex == tc.index {
				got = &r
			}
		}
		if got == nil || got.AuthID != tc.id || got.SuggestedModel != tc.suggested {
			t.Fatalf("wrong resource for %s/%s: %#v", tc.provider, tc.index, got)
		}
		c := &PolicyCandidate{Provider: tc.provider, AuthIndex: tc.index}
		if id, e := liveIDForCandidateV10(c); e != nil || id != tc.id {
			t.Fatalf("migrated policy failed exact binding: %q, %v", id, e)
		}
		if p, ok := configPriorityForCandidateV10(c); !ok || p != tc.priority {
			t.Fatalf("priority = %d,%t, want %d", p, ok, tc.priority)
		}
		c.AuthID = "stale-id"
		if _, ok := configPriorityForCandidateV10(c); ok {
			t.Fatal("must not lend diagnostics to a stale exact ID")
		}
	}
}

func TestCPAConfigV8ExplicitEmptyWins(t *testing.T) {
	const raw = `
access: {api-keys: []}
api-keys: {codex: []}
codex-api-key: [{api-key: old-key, base-url: https://old.example}]
gemini-api-key: [{api-key: other-family-key}]
`
	keys, res := parseCPAConfig(raw)
	if len(keys) != 0 || len(res) != 1 || res[0].Provider != "gemini" {
		t.Fatalf("wrong precedence: %d client keys, %#v resources", len(keys), res)
	}
	keys, _ = parseCPAConfig("api-keys: [old-key]\naccess: {api-keys: []}")
	if len(keys) != 0 {
		t.Fatal("explicit empty v8 client list must override the legacy list")
	}
}

func TestCPAInvalidConfigDoesNotLeakYAMLError(t *testing.T) {
	withCPAConfigPathForTest(t, "api-keys: {codex: secret-protected-value}")
	keys, _, errText := currentEnvironment()
	if len(keys) != 0 || errText == "" || strings.Contains(errText, "secret-protected-value") {
		t.Fatalf("invalid config not surfaced safely: %q", errText)
	}
}

func TestCPAConfigV8PoolIdentityFollowsGroupAndKey(t *testing.T) {
	const raw = `
api-keys:
  codex:
    - base-url: https://example.invalid
      connection-pool: {enabled: true, idle-conn-timeout: 30s, max-idle-conns-per-host: 4}
      keys:
        - api-key: test-one
          connection-pool: {max-idle-conns-per-host: 8}
  openai-compatibility:
    - name: test
      base-url: https://example.invalid
      connection-pool: {enabled: true, idle-conn-timeout: 30s, max-idle-conns-per-host: 4}
      keys:
        - api-key: test-two
          connection-pool: {enabled: false}
`
	inv, err := readCPAConfigInventory(raw)
	if err != nil || len(inv.resources) != 2 {
		t.Fatalf("inventory: %d resources, %v", len(inv.resources), err)
	}
	wants := []string{
		stableID("codex:apikey", "test-one", "https://example.invalid", "", "", "", "pool:true\x0030s\x008"),
		stableID("openai-compatibility:test", "test-two", "https://example.invalid", "", "pool:false\x0030s\x004"),
	}
	for i, r := range inv.resources {
		if r.liveID != wants[i] {
			t.Fatalf("wrong pool identity at %d: %q", i, r.liveID)
		}
	}
}
