package main

import (
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

func TestLegacySyntheticAPIAuthIndexResolvesExactCurrentStableID(t *testing.T) {
	config := "codex-api-key:\n  - api-key: sk-up\n    base-url: https://up.example/v1\n    prefix: plus\n"
	withCPAConfigPathForTest(t, config)
	_, resources := parseCPAConfig(config)
	if len(resources) != 1 || resources[0].AuthIndex == "" {
		t.Fatalf("config resources = %#v", resources)
	}
	stale := resources[0].AuthIndex
	want := stableID("codex:apikey", "sk-up", "https://up.example/v1", "", "plus", "")
	id, err := resolveLegacySyntheticAuthIDV8(stale, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if id != want {
		t.Fatalf("resolved=%q, want exact CPA StableID %q", id, want)
	}
}

func TestLegacySyntheticAPIAuthIndexDistinguishesSameBaseURLKeys(t *testing.T) {
	config := "codex-api-key:\n  - api-key: sk-one\n    base-url: https://up.example/v1\n    prefix: plus\n  - api-key: sk-two\n    base-url: https://up.example/v1\n    prefix: plus\n"
	withCPAConfigPathForTest(t, config)
	_, resources := parseCPAConfig(config)
	if len(resources) != 2 {
		t.Fatalf("config resources=%#v", resources)
	}
	stale := resources[1].AuthIndex
	want := stableID("codex:apikey", "sk-two", "https://up.example/v1", "", "plus", "")
	id, err := resolveLegacySyntheticAuthIDV8(stale, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if id != want {
		t.Fatalf("resolved=%q, want exact second credential %q", id, want)
	}
	candidates := []any{
		map[string]any{"ID": stableID("codex:apikey", "sk-one", "https://up.example/v1", "", "plus", ""), "Provider": "codex"},
		map[string]any{"ID": want, "Provider": "codex"},
	}
	if !schedulerCandidateEligible(candidates, id, stale, "codex") {
		t.Fatal("exact second runtime ID must be selectable from live scheduler candidates")
	}
}

func TestLegacySyntheticAPIAuthIndexIncludesProxyPrefixAndHeaders(t *testing.T) {
	config := "codex-api-key:\n  - api-key: sk-up\n    base-url: https://up.example/v1\n    proxy-url: http://proxy.local:8080\n    prefix: /plus/\n    headers:\n      X-Z: z\n      X-A: a\n"
	withCPAConfigPathForTest(t, config)
	_, resources := parseCPAConfig(config)
	stale := resources[0].AuthIndex
	id, err := resolveLegacySyntheticAuthIDV8(stale, "codex")
	if err != nil {
		t.Fatal(err)
	}
	want := stableID("codex:apikey", "sk-up", "https://up.example/v1", "http://proxy.local:8080", "plus", "X-A\x00a\x00X-Z\x00z\x00")
	if id != want {
		t.Fatalf("runtime ID=%q, want %q", id, want)
	}
}

func TestLegacySyntheticAPIAuthIDMustExistInLiveSchedulerCandidates(t *testing.T) {
	config := "codex-api-key:\n  - api-key: sk-intended\n    base-url: https://up.example/v1\n"
	withCPAConfigPathForTest(t, config)
	_, resources := parseCPAConfig(config)
	stale := resources[0].AuthIndex
	intended, err := resolveLegacySyntheticAuthIDV8(stale, "codex")
	if err != nil {
		t.Fatal(err)
	}
	unrelated := stableID("codex:apikey", "sk-other", "https://up.example/v1", "", "", "")
	candidates := []any{map[string]any{"ID": unrelated, "Provider": "codex"}}
	if schedulerCandidateEligible(candidates, intended, stale, "codex") {
		t.Fatal("same-provider live credential must not substitute for missing exact AuthID")
	}
}

func TestLegacySyntheticAPIAuthIndexRejectsAmbiguousOldIdentity(t *testing.T) {
	config := "codex-api-key:\n  - api-key: sk-up\n    base-url: https://up.example/v1\n  - api-key: sk-up\n    base-url: https://up.example/v1\n"
	withCPAConfigPathForTest(t, config)
	_, resources := parseCPAConfig(config)
	if len(resources) != 2 || resources[0].AuthIndex != resources[1].AuthIndex {
		t.Fatalf("expected duplicate legacy identity, got %#v", resources)
	}
	if id, err := resolveLegacySyntheticAuthIDV8(resources[0].AuthIndex, "codex"); err == nil || id != "" {
		t.Fatalf("ambiguous legacy identity must fail closed: id=%q err=%v", id, err)
	}
}
