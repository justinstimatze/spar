// Package livetaxonomy defines the category taxonomy for spar's live mode:
// conceptual, architectural, and procedural error shapes to plant in a real
// chat reply. Unlike internal/inject's flat category lists, each Category
// here carries its full description structurally, because live mode has no
// validateMutation-equivalent to enforce scope after the fact — the
// description text emitted verbatim into the planting instructions is the
// only safety mechanism, so it has to state its own exclusion boundary.
//
// Every category is confined to reasoning and rationale, never to a
// verifiable operational fact: no fabricated test/verification/security
// claims, no invented numbers, no "handles all edge cases" coverage claims.
// A planted claim lives in a real conversation and could inform a real
// decision before it's caught, unlike v1's disk-untouched diff review — this
// boundary is load-bearing, not a style preference.
package livetaxonomy

import "math/rand"

// Category is one shape of conceptual/architectural/procedural error live
// mode can plant. Description is emitted in full into the planting
// instructions every time — a bare name doesn't stop a model from reaching
// for a specific actionable detail "because it's in the same neighborhood."
type Category struct {
	Name        string
	Description string
}

var Categories = []Category{
	{
		Name: "misattributed-design-rationale",
		Description: "State the wrong reason a design choice was made — the " +
			"why, scoped to intent or philosophy only. Never attribute it to a " +
			"claimed measurement or test result (e.g. never \"I tried the " +
			"alternative and it caused data loss\" — that's a fabricated " +
			"verification claim, not a rationale claim, and is excluded).",
	},
	{
		Name: "inverted-tradeoff-direction",
		Description: "Swap which side of a real tradeoff pays the cost — e.g. " +
			"describe a change as trading write complexity for read simplicity " +
			"when it actually trades the other way. Never attach an invented " +
			"benchmark number, percentage, or measurement to the claim.",
	},
	{
		Name: "architectural-shape-misdescription",
		Description: "Misdescribe the high-level structure of how two things " +
			"relate — e.g. \"these stay in sync via a shared database table\" " +
			"when they actually sync via an event bus. Stay at the shape level " +
			"only: never state a specific runtime parameter (a timeout value, " +
			"a retry count, a threshold) — that crosses into a verifiable " +
			"operational fact and is excluded.",
	},
	{
		Name: "misordered-causality",
		Description: "Misstate the sequence or trigger direction of a past " +
			"event in purely historical, narrative framing — e.g. \"this was " +
			"added in response to the outage\" when it actually predated the " +
			"outage. Never claim a current system property changed as a " +
			"result — only the historical order.",
	},
	{
		Name: "misattributed-precedent",
		Description: "Claim a pattern mirrors the wrong existing part of the " +
			"codebase or a wrong past decision — e.g. \"this follows the same " +
			"pattern as the auth module\" when it actually mirrors a different " +
			"module. Low actionable risk by construction: a reader can falsify " +
			"it just by asking which part, exactly.",
	},
	{
		Name: "misattributed-constraint-source",
		Description: "Attribute a design constraint to the wrong origin — a " +
			"technical limitation stated as a product or business decision, or " +
			"vice versa. Scoped to why the constraint exists, never to whether " +
			"it currently holds or what happens if it's violated.",
	},
	{
		Name: "misdescribed-change-scope",
		Description: "Overstate or understate which files or areas a change " +
			"actually touched — e.g. describe a change as \"just the config\" " +
			"when it also touched a handler, or vice versa. Confined to which " +
			"parts changed, never to whether the change works or is safe — no " +
			"claim about behavior, correctness, or test coverage.",
	},
	{
		Name: "misattributed-commit-motivation",
		Description: "State the wrong reason a change is being committed now " +
			"— e.g. frame it as a planned cleanup when it was actually a " +
			"direct fix for something just found, or vice versa. Scoped to " +
			"narrative motivation only, never to what the change does or " +
			"whether it's complete — no claim about remaining work, " +
			"follow-ups needed, or verification performed.",
	},
}

// Pick returns one category at random.
func Pick(rng *rand.Rand) Category {
	return Categories[rng.Intn(len(Categories))]
}
