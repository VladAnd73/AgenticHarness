package dream

import (
	"strings"
	"testing"
)

// requirePhrases matches against the brief with runs of whitespace
// collapsed, so a phrase still counts when a reflow wraps it across two
// lines. The test is meant to catch a rule being deleted, not a line
// being rewrapped.
func requirePhrases(t *testing.T, name, brief string, want []string) {
	t.Helper()
	flat := strings.Join(strings.Fields(brief), " ")
	for _, w := range want {
		if !strings.Contains(flat, strings.Join(strings.Fields(w), " ")) {
			t.Errorf("%s brief is missing %q", name, w)
		}
	}
}

func TestBriefsCarryTheLoadBearingRules(t *testing.T) {
	requirePhrases(t, "proposer", ProposerBrief, []string{
		"You may not write anything",
		"evidence packet",
		"operator-preference",
		"known-claims.md",
		"An empty night is a result",
	})
	requirePhrases(t, "reviewer", ReviewerBrief, []string{
		"Re-derive, never trust",
		"Documentation over recall",
		"Default to reject",
		"stale",
		"You may not repair the packet",
	})
}

// Both briefs ship inside the binary to every consumer of this harness,
// so a phrase that fits only the machine they were written on is a leak.
func TestBriefsNameNoProjectAndNoHost(t *testing.T) {
	for name, brief := range map[string]string{
		"proposer": ProposerBrief, "reviewer": ReviewerBrief,
	} {
		for _, banned := range []string{
			"marketer", "crm-gateway", "spore", "nixos", "/home/", "vlad", "@",
		} {
			if strings.Contains(strings.ToLower(brief), banned) {
				t.Errorf("%s brief leaks %q; kernel assets stay generic", name, banned)
			}
		}
	}
}

func TestBriefsAreAsciiOnly(t *testing.T) {
	for name, brief := range map[string]string{
		"proposer": ProposerBrief, "reviewer": ReviewerBrief,
	} {
		for i, r := range brief {
			if r > 127 {
				t.Errorf("%s brief has non-ASCII rune %q at byte %d", name, r, i)
			}
		}
	}
}

// A median transcript does not fit in the reading agent's context, so the
// brief has to bound the read and forbid the claim of a full one.
func TestProposerBriefBoundsTheDeepRead(t *testing.T) {
	requirePhrases(t, "proposer", ProposerBrief, []string{
		"Never read a transcript end to end",
		"anchor",
		"truncates every entry",
		"Never write that you read a session in full",
		"coverage",
	})
}

// The ledger fingerprints a claim by its exact wording, so a reworded
// repeat never reaches the two-session bar. Constraining the wording is
// half the fix; reusing a wording already on file is the other half.
func TestProposerBriefRequiresCanonicalFormAndReuse(t *testing.T) {
	requirePhrases(t, "proposer", ProposerBrief, []string{
		"canonical form",
		"copy that claim line word for word",
		"one sentence, present tense",
		"use its type unchanged",
	})
}

// Gate waves an operator preference through on first sighting and nothing
// in Go checks that the operator ever said it.
func TestProposerBriefGuardsTheOperatorPreferenceType(t *testing.T) {
	requirePhrases(t, "proposer", ProposerBrief, []string{
		"verbatim",
		"Operator messages",
		"is never this type",
	})
}

// The corpus contains this feature's own design sessions, so prose about a
// lesson sits next to the lesson itself.
func TestBothBriefsSeparateDiscussedFromDemonstrated(t *testing.T) {
	for name, brief := range map[string]string{
		"proposer": ProposerBrief, "reviewer": ReviewerBrief,
	} {
		requirePhrases(t, name, brief, []string{"DISCUSSED", "DEMONSTRATED"})
	}
}

func TestReviewerBriefConfirmedRequiresProof(t *testing.T) {
	requirePhrases(t, "reviewer", ReviewerBrief, []string{
		"`confirmed` requires a non-empty `proof`",
	})
}
