package main

import (
	"os"
	"strings"
	"testing"
)

func TestModelMatches(t *testing.T) {
	cases := []struct {
		pats  []string
		model string
		want  bool
	}{
		{[]string{"*"}, "gpt-5.6-luna", true},
		{[]string{"gpt-5.6-luna"}, "gpt-5.6-luna", true},
		{[]string{"claude-*"}, "claude-opus-4.6", true},
		{[]string{"gpt-5.?"}, "gpt-5.6", true},
		{[]string{"gpt-5.6-luna"}, "gpt-5.6-sol", false},
	}
	for _, c := range cases {
		if got := modelMatches(c.pats, c.model); got != c.want {
			t.Fatalf("modelMatches(%v,%q)=%v want %v", c.pats, c.model, got, c.want)
		}
	}
}

func TestNormalizeMatchModels(t *testing.T) {
	got := normalizeMatchModels(" gpt-5.6-luna, claude-* ; gpt-5.6-luna ")
	if strings.Join(got, "|") != "gpt-5.6-luna|claude-*" {
		t.Fatalf("got %v", got)
	}
	if x := normalizeMatchModels(""); len(x) != 1 || x[0] != "*" {
		t.Fatalf("empty=%v", x)
	}
}

func TestParseCPAConfig(t *testing.T) {
	cfg := `
api-keys:
  - "sk-down-1111"
  - sk-down-2222
codex-api-key:
  - api-key: "sk-up-codex"
    prefix: plus
    base-url: "https://codex.example/v1"
    models:
      - name: gpt-5.6-luna
        alias: luna
openai-compatibility:
  - name: loveapi
    prefix: love
    base-url: https://love.example/v1
    api-key-entries:
      - api-key: sk-love
    models:
      - name: gpt-5.6-luna
        alias: luna
`
	keys, res := parseCPAConfig(cfg)
	if len(keys) != 2 {
		t.Fatalf("keys=%d %#v", len(keys), keys)
	}
	if len(res) != 2 {
		t.Fatalf("res=%d %#v", len(res), res)
	}
	var codex, love *apiResource
	for i := range res {
		if res[i].Provider == "codex" {
			codex = &res[i]
		}
		if res[i].Provider == "openai-compatible-loveapi" {
			love = &res[i]
		}
	}
	if codex == nil || love == nil {
		t.Fatalf("resources=%#v", res)
	}
	if codex.AuthIndex != stableAuthIndex("codex-api-key:https://codex.example/v1+sk-up-codex") {
		t.Fatalf("codex authIndex=%s", codex.AuthIndex)
	}
	if codex.SuggestedModel != "plus/luna" {
		t.Fatalf("suggested=%s", codex.SuggestedModel)
	}
	if love.AuthIndex != stableAuthIndex("openai-compatibility:https://love.example/v1+sk-love") {
		t.Fatalf("love authIndex=%s", love.AuthIndex)
	}
}

func TestStateMigration(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/state.json"
	raw := `{"version":2,"routes":{"r":{"id":"r","name":"old","key_fingerprint":"abc","enabled":true,"candidates":[{"id":"c","name":"x","provider":"codex","model":"gpt-5.6-luna","enabled":true}]}}}`
	if err := osWriteFile(p, []byte(raw)); err != nil {
		t.Fatal(err)
	}
	st, err := loadState(p, pluginConfig{})
	if err != nil {
		t.Fatal(err)
	}
	r := st.Routes["r"]
	if len(r.MatchModels) != 1 || r.MatchModels[0] != "*" {
		t.Fatalf("match=%v", r.MatchModels)
	}
	if r.Candidates[0].OverrideModel != "gpt-5.6-luna" || r.Candidates[0].Model != "" {
		t.Fatalf("candidate=%+v", r.Candidates[0])
	}
}

func osWriteFile(path string, b []byte) error { return os.WriteFile(path, b, 0600) }
