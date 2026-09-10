package main

import "testing"

func TestAuthIDFromEntriesUsesHostAuthID(t *testing.T) {
	entries := []hostAuthEntry{
		{ID: "real-auth-id", AuthIndex: "idx-a", Provider: "codex"},
	}
	if got := authIDFromEntries(entries, "idx-a", "codex"); got != "real-auth-id" {
		t.Fatalf("auth id = %q, want real-auth-id", got)
	}
}

func TestAuthIDFromEntriesDisambiguatesDuplicateIndexByProvider(t *testing.T) {
	entries := []hostAuthEntry{
		{ID: "claude-auth", AuthIndex: "shared", Provider: "claude"},
		{ID: "codex-auth", AuthIndex: "shared", Provider: "codex"},
	}
	if got := authIDFromEntries(entries, "shared", "codex"); got != "codex-auth" {
		t.Fatalf("auth id = %q, want codex-auth", got)
	}
}

func TestAuthIDFromEntriesDoesNotGuessAmbiguousIndex(t *testing.T) {
	entries := []hostAuthEntry{
		{ID: "a", AuthIndex: "shared", Provider: "claude"},
		{ID: "b", AuthIndex: "shared", Provider: "gemini"},
	}
	if got := authIDFromEntries(entries, "shared", "codex"); got != "" {
		t.Fatalf("auth id = %q, want empty for ambiguous unmatched provider", got)
	}
}
