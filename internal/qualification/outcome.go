package qualification

// Outcome describes a completed update or the reason a customer was skipped.
// API failures are returned separately as errors, with NoOutcome.
type Outcome string

const (
	NoOutcome          Outcome = ""
	Updated            Outcome = "updated"
	Unchanged          Outcome = "unchanged"
	MissingEmail       Outcome = "missing_email"
	UnevaluatedSegment Outcome = "unevaluated_segment"
	AmbiguousText      Outcome = "ambiguous_text"
	MissingHubSpot     Outcome = "missing_hubspot"
	AmbiguousHubSpot   Outcome = "ambiguous_hubspot"
)
