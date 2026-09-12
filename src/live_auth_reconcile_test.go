package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func withCPAConfigPathForTest(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	runtimeState.Lock()
	old := runtimeState.configPath
	runtimeState.configPath = path
	runtimeState.Unlock()
	t.Cleanup(func() {
		runtimeState.Lock()
		runtimeState.configPath = old
		runtimeState.Unlock()
	})
	return path
}

func TestLegacySyntheticAPIAuthIndexResolvesToLiveCredential(t *testing.T) {
	config := "codex-api-key:\n  - api-key: sk-up\n    base-url: https://up.example/v1\n    prefix: plus\n"
	withCPAConfigPathForTest(t, config)
	_, resources := parseCPAConfig(config)
	if len(resources) != 1 || resources[0].AuthIndex == "" {
		t.Fatalf("config resources = %#v", resources)
	}
	stale := resources[0].AuthIndex

	raw, _ := json.Marshal(liveAuthListResponseV8{Files: []liveAuthEntryV8{{
		ID: "codex-runtime-id", AuthIndex: "live-auth-index", Provider: "codex",
		BaseURL: "https://up.example/v1/", RuntimeOnly: true,
	}}})
	id, idx, err := resolveLegacySyntheticAuthV8(raw, stale, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if id != "codex-runtime-id" || idx != "live-auth-index" {
		t.Fatalf("resolved=(%q,%q), want live credential", id, idx)
	}
}

func TestLegacySyntheticAPIAuthIndexUsesExactRuntimeIDForSameBaseURL(t *testing.T) {
	config := "codex-api-key:\n  - api-key: sk-one\n    base-url: https://up.example/v1\n    prefix: plus\n  - api-key: sk-two\n    base-url: https://up.example/v1\n    prefix: plus\n"
	withCPAConfigPathForTest(t, config)
	_, resources := parseCPAConfig(config)
	if len(resources) != 2 {
		t.Fatalf("config resources=%#v", resources)
	}
	stale := resources[1].AuthIndex
	exact := legacyCurrentRuntimeIDsV8(config, stale, "codex")
	if len(exact) != 1 {
		t.Fatalf("exact runtime IDs=%#v", exact)
	}
	raw, _ := json.Marshal(liveAuthListResponseV8{Files: []liveAuthEntryV8{
		{ID: stableID("codex:apikey", "sk-one", "https://up.example/v1", "", "plus", ""), AuthIndex: "live-one", Provider: "codex", BaseURL: "https://up.example/v1", RuntimeOnly: true},
		{ID: exact[0], AuthIndex: "live-two", Provider: "codex", BaseURL: "https://up.example/v1", RuntimeOnly: true},
	}})
	id, idx, err := resolveLegacySyntheticAuthV8(raw, stale, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if id != exact[0] || idx != "live-two" {
		t.Fatalf("resolved=(%q,%q), want exact second credential (%q,live-two)", id, idx, exact[0])
	}
}

func TestLegacySyntheticAPIAuthIndexIncludesProxyPrefixAndHeaders(t *testing.T) {
	config := "codex-api-key:\n  - api-key: sk-up\n    base-url: https://up.example/v1\n    proxy-url: http://proxy.local:8080\n    prefix: /plus/\n    headers:\n      X-Z: z\n      X-A: a\n"
	withCPAConfigPathForTest(t, config)
	_, resources := parseCPAConfig(config)
	stale := resources[0].AuthIndex
	ids := legacyCurrentRuntimeIDsV8(config, stale, "codex")
	want := stableID("codex:apikey", "sk-up", "https://up.example/v1", "http://proxy.local:8080", "plus", "X-A\x00a\x00X-Z\x00z\x00")
	if len(ids) != 1 || ids[0] != want {
		t.Fatalf("runtime IDs=%#v, want %q", ids, want)
	}
}

func TestLegacySyntheticAPIAuthIndexRefusesAmbiguousLiveCredentials(t *testing.T) {
	config := "codex-api-key:\n  - api-key: sk-up\n    base-url: https://up.example/v1\n"
	withCPAConfigPathForTest(t, config)
	_, resources := parseCPAConfig(config)
	stale := resources[0].AuthIndex
	raw, _ := json.Marshal(liveAuthListResponseV8{Files: []liveAuthEntryV8{
		{ID: "runtime-a", AuthIndex: "live-a", Provider: "codex", BaseURL: "https://up.example/v1", RuntimeOnly: true},
		{ID: "runtime-b", AuthIndex: "live-b", Provider: "codex", BaseURL: "https://up.example/v1", RuntimeOnly: true},
	}})
	if _, _, err := resolveLegacySyntheticAuthV8(raw, stale, "codex"); err == nil {
		t.Fatal("ambiguous live credentials must fail closed")
	}
}

func TestLegacySyntheticAPIAuthIndexNeverMatchesOAuth(t *testing.T) {
	config := "codex-api-key:\n  - api-key: sk-up\n    base-url: https://up.example/v1\n"
	withCPAConfigPathForTest(t, config)
	_, resources := parseCPAConfig(config)
	stale := resources[0].AuthIndex
	raw, _ := json.Marshal(liveAuthListResponseV8{Files: []liveAuthEntryV8{{
		ID: "oauth-a", AuthIndex: "oauth-index", Provider: "codex",
		BaseURL: "https://up.example/v1", RuntimeOnly: false,
	}}})
	id, idx, err := resolveLegacySyntheticAuthV8(raw, stale, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if id != "" || idx != "" {
		t.Fatalf("OAuth credential must not satisfy API reconciliation: (%q,%q)", id, idx)
	}
}
