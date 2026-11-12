package route

// Detail is the optional category a driver client reports with an attempt
// (outcomeDetail). It never replaces failureReason: the text stays required for a
// FAILED attempt, so a client that sends only the text keeps working. Empty means
// "not reported" (a legacy client); it is NOT the same as DetailOther (ADR 0014).
type Detail string

// The four outcome details.
const (
	// DetailNotHome: a FAILED attempt, nobody answered at the point (counted).
	DetailNotHome Detail = "NOT_HOME"
	// DetailAccessProblem: a FAILED attempt, the point could not be reached (counted).
	DetailAccessProblem Detail = "ACCESS_PROBLEM"
	// DetailOther: a FAILED attempt for any other reason (tallied, never counted).
	DetailOther Detail = "OTHER"
	// DetailSafePlaceUsed: a DELIVERED attempt left in a safe place.
	DetailSafePlaceUsed Detail = "SAFE_PLACE_USED"
)

// Valid reports whether d is allowed for outcome o: for FAILED the set is {"",
// NOT_HOME, ACCESS_PROBLEM, OTHER}; for DELIVERED it is {"", SAFE_PLACE_USED}.
func (d Detail) Valid(o Outcome) bool {
	if d == "" {
		return true
	}
	switch o {
	case OutcomeFailed:
		return d == DetailNotHome || d == DetailAccessProblem || d == DetailOther
	case OutcomeDelivered:
		return d == DetailSafePlaceUsed
	}
	return false
}
