package main

import "testing"

func TestLegacySyntheticAPIAuthIndexIncludesFlowStyleHeaders(t *testing.T) {
	config := "codex-api-key:\n  - api-key: sk-up\n    base-url: https://up.example/v1\n    proxy-url: http://proxy.local:8080\n    prefix: /plus/\n    headers: {X-Z: z, X-A: a}\n"
	withCPAConfigPathForTest(t, config)
	_, resources := parseCPAConfig(config)
	if len(resources) != 1 || resources[0].AuthIndex == "" {
		t.Fatalf("config resources = %#v", resources)
	}
	id, err := resolveLegacySyntheticAuthIDV8(resources[0].AuthIndex, "codex")
	if err != nil {
		t.Fatal(err)
	}
	want := stableID("codex:apikey", "sk-up", "https://up.example/v1", "http://proxy.local:8080", "plus", "X-A\x00a\x00X-Z\x00z\x00")
	if id != want {
		t.Fatalf("flow-style runtime ID=%q, want %q", id, want)
	}
}

func TestLegacySyntheticAPIAuthIndexIncludesMultilineFlowStyleHeaders(t *testing.T) {
	config := "codex-api-key:\n  - api-key: sk-up\n    base-url: https://up.example/v1\n    proxy-url: http://proxy.local:8080\n    prefix: /plus/\n    headers: {\n      X-Z: z,\n      X-A: \"a,b:c\"\n    }\n"
	withCPAConfigPathForTest(t, config)
	_, resources := parseCPAConfig(config)
	if len(resources) != 1 || resources[0].AuthIndex == "" {
		t.Fatalf("config resources = %#v", resources)
	}
	id, err := resolveLegacySyntheticAuthIDV8(resources[0].AuthIndex, "codex")
	if err != nil {
		t.Fatal(err)
	}
	want := stableID("codex:apikey", "sk-up", "https://up.example/v1", "http://proxy.local:8080", "plus", "X-A\x00a,b:c\x00X-Z\x00z\x00")
	if id != want {
		t.Fatalf("multiline flow-style runtime ID=%q, want %q", id, want)
	}
}

func TestParseFlowStringMapV8PreservesQuotedCommaAndColon(t *testing.T) {
	got := parseFlowStringMapV8(`{X-A: "a,b:c", 'X-B': 'x:y,z'}`)
	if got["X-A"] != "a,b:c" || got["X-B"] != "x:y,z" || len(got) != 2 {
		t.Fatalf("flow headers = %#v", got)
	}
}
