package splice

import "testing"

// TestPartyHintFor_RejectsHyphenInName covers the bug discovered by the
// BIT-117 integration test: instance names with hyphens (e.g.
// `ci-local`) were yielding party hints like
// `app_user_ci-local-localparty-1` (after Splice's env-file prefix),
// which Splice strictly rejects as INVALID_ARGUMENT because it expects
// exactly three hyphen-separated segments — <organization>-<function>-
// <enumerator>.
//
// The fix is to substitute underscores for hyphens in the name
// component before assembling the hint. Single-segment names are
// unchanged.
func TestPartyHintFor(t *testing.T) {
	cases := []struct {
		name, want string
	}{
		// Single-segment names pass through unchanged.
		{"alice", "alice-localparty-1"},
		{"diag", "diag-localparty-1"},
		{"ci", "ci-localparty-1"},

		// Hyphenated names get underscored — the fix.
		{"ci-local", "ci_local-localparty-1"},
		{"my-test-stack", "my_test_stack-localparty-1"},

		// Multiple hyphens collapse to multiple underscores.
		{"a-b-c-d", "a_b_c_d-localparty-1"},
	}
	for _, c := range cases {
		got := PartyHintFor(c.name)
		if got != c.want {
			t.Errorf("PartyHintFor(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestPartyHintFor_LocksSpliceContract is a regression guard with an
// explanatory error so a future refactor that revives the hyphen issue
// is caught with the actual root cause spelled out.
func TestPartyHintFor_LocksSpliceContract(t *testing.T) {
	// With Splice's env/app-user-auth-on.env line
	//   APP_USER_PARTY_HINT=app_user_${PARTY_HINT}
	// the final hint Splice's validator sees is `app_user_<our>`.
	// That string must split on '-' into exactly 3 parts to satisfy
	// `<organization>-<function>-<enumerator>`.
	for _, name := range []string{"alice", "ci-local", "my-test-stack"} {
		final := "app_user_" + PartyHintFor(name)
		segments := 1
		for _, ch := range final {
			if ch == '-' {
				segments++
			}
		}
		if segments != 3 {
			t.Errorf("PartyHintFor(%q) → app_user_%s has %d hyphen-segments, want 3 (Splice would reject with INVALID_ARGUMENT)",
				name, PartyHintFor(name), segments)
		}
	}
}
