package http

import (
	stdhttp "net/http"
	"strconv"
)

// ReplayEntry is one problem type of the service, as the replay-class tests see
// it: its slug, status and declared class, and how to answer it through the
// production error writer.
type ReplayEntry struct {
	Slug   string
	Status int
	Class  string
	err    error
	p      problem
}

// Refuse answers the entry exactly as production does: through writeError (the
// catalogue match) when the entry has an error, else through writeProblem, so
// the class marking runs.
func (e ReplayEntry) Refuse(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if e.err != nil {
		writeError(w, r, e.err)
		return
	}
	writeProblem(w, r, e.p, "refused")
}

func entryOf(p problem, err error) ReplayEntry {
	return ReplayEntry{Slug: p.slug, Status: p.status, Class: string(p.class), err: err, p: p}
}

// ReplayCatalogue lists every problem type this adapter can answer, once per
// slug and status: each catalogue entry plus the problems written outside the
// catalogue slice (the internal error, the readiness 503, the unrouted path,
// the 405, the middleware's own key problems and the paging problems).
func ReplayCatalogue() []ReplayEntry {
	out := make([]ReplayEntry, 0, len(problemCatalogue)+10)
	seen := map[string]bool{}
	add := func(p problem, err error) {
		if id := p.slug + "/" + strconv.Itoa(p.status); !seen[id] {
			seen[id] = true
			out = append(out, entryOf(p, err))
		}
	}
	for _, e := range problemCatalogue {
		add(e.p, e.err)
	}
	for _, p := range []problem{
		internalProblem, dependencyUnavailable, notFoundResource, methodNotAllowedProblem,
		idempotencyKeyRequired, idempotencyKeyInvalid, idempotencyKeyReused,
		invalidLimit, invalidCursor, unknownQueryParameter,
	} {
		add(p, nil)
	}
	return out
}

// SyntheticOutcomeEntry is a problem of the OUTCOME class that is NOT in the
// catalogue: no real problem type of this service is an OUTCOME (ADR 0019), so
// the mechanism (a stored refusal keeps the handler's writes) is proven with
// this stand-in only.
func SyntheticOutcomeEntry() ReplayEntry {
	return entryOf(problem{status: stdhttp.StatusConflict, slug: "synthetic-outcome-probe", title: "Synthetic outcome probe", class: replayOutcome}, nil)
}
