package main

import "testing"

func TestClientAffinityStrictAllowsNativeAPIAndAllOAuth(t *testing.T) {
	p := &Policy{ClientAffinity: clientAffinityStrict, ClientType: "claude"}
	cases := []struct {
		c    *PolicyCandidate
		want bool
	}{
		{&PolicyCandidate{ResourceKind: "API", Provider: "claude"}, true},
		{&PolicyCandidate{ResourceKind: "API", Provider: "codex"}, false},
		{&PolicyCandidate{ResourceKind: "OAuth", Provider: "codex"}, true},
		{&PolicyCandidate{ResourceKind: "OAuth", Provider: "claude"}, true},
	}
	for _, tc := range cases {
		if got := candidateAllowedByClientAffinityV1(p, tc.c); got != tc.want {
			t.Fatalf("candidate %+v allowed=%v want %v", tc.c, got, tc.want)
		}
	}
}

func TestClientAffinityOffDoesNotFilter(t *testing.T) {
	p := &Policy{ClientAffinity: clientAffinityOff}
	xs := []*PolicyCandidate{{Provider: "codex", ResourceKind: "API"}, {Provider: "claude", ResourceKind: "OAuth"}}
	if got := filterCandidatesByClientAffinityV1(p, xs); len(got) != 2 {
		t.Fatalf("len=%d", len(got))
	}
}

func TestClientAffinityUnknownModeFailsClosed(t *testing.T) {
	p := &Policy{ClientAffinity: "mixed", ClientProvider: "claude"}
	xs := []*PolicyCandidate{{ResourceKind: "API", Provider: "claude"}, {ResourceKind: "OAuth", Provider: "codex"}}
	if got := filterCandidatesByClientAffinityV1(p, xs); len(got) != 0 {
		t.Fatalf("unknown affinity must fail closed, got %d candidates", len(got))
	}
	for _, candidate := range xs {
		if candidateAllowedByClientAffinityV1(p, candidate) {
			t.Fatalf("unknown affinity unexpectedly allowed candidate %+v", candidate)
		}
	}
}

func TestClientTypePersistsWhenAffinityOff(t *testing.T) {
	p := &Policy{ClientAffinity: clientAffinityOff, ClientType: "claude", ClientProvider: "claude"}
	normalizeClientAffinityV1(p)
	if p.ClientType != "claude" {
		t.Fatalf("client type = %q, want claude", p.ClientType)
	}
	if p.ClientProvider != "" {
		t.Fatalf("client provider = %q, want empty when affinity off", p.ClientProvider)
	}
}

func TestV080StrictProviderMigratesToClientType(t *testing.T) {
	p := &Policy{ClientAffinity: clientAffinityStrict, ClientProvider: "claude"}
	normalizeClientAffinityV1(p)
	if p.ClientType != "claude" || p.ClientProvider != "claude" {
		t.Fatalf("migration failed: %+v", p)
	}
}
