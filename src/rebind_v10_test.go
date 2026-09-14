package main

import "testing"

func TestRebindKeepsExistingLiveIdentity(t *testing.T) {
	st := V4State{Policies: map[string]*Policy{"fp": {KeyFingerprint: "fp", Rules: []*PolicyRule{{ID: "r1", Candidates: []*PolicyCandidate{{ID: "c1", Provider: "codex", ResourceID: liveBoundResourceIDV10("codex", "old-id"), AuthID: "old-id", AuthIndex: "same-index", Enabled: true}}}}}}}
	resources := []apiResource{{ID: liveBoundResourceIDV10("codex", "new-id"), Kind: "API", Provider: "codex", AuthID: "new-id", AuthIndex: "same-index"}}
	rebindPoliciesV4(&st, resources)
	got := st.Policies["fp"].Rules[0].Candidates[0]
	if got.AuthID != "old-id" {
		t.Fatalf("identity changed unexpectedly: %q", got.AuthID)
	}
}
