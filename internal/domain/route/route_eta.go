package route

import "time"

// The ETA window constants of ADR 0013.
const (
	// EtaWindowWidth is the widest ETA window handed to a shopper.
	EtaWindowWidth = 2 * time.Hour
	// MinEtaSpan is the least time the wave's delivery window must have left
	// at the start instant for an ETA to be given at all.
	MinEtaSpan = 30 * time.Minute
	// EtaGrain is the granularity of the window bounds.
	EtaGrain = time.Minute
)

// StopRef is one stop as the ETA computation sees it: the stop number, the
// opaque stopKey (pkg:<trackingNumber> when the package has no label data) and
// the packages of the stop in stop order.
type StopRef struct {
	No              int
	StopKey         string
	TrackingNumbers []string
}

// EtaInput is what EtaWindows reads: the start instant, the wave's delivery
// window and the stops in stop order.
type EtaInput struct {
	Now         time.Time
	WindowStart time.Time
	WindowEnd   time.Time
	Stops       []StopRef
}

// StopEta is the ETA window of one stop.
type StopEta struct {
	No              int
	StopKey         string
	TrackingNumbers []string
	Start           time.Time
	End             time.Time
}

// EtaWindows spreads the stops linearly over what is left of the wave's
// delivery window and gives each a window of at most EtaWindowWidth centred on
// its share of the span (ADR 0013). It is deterministic, uses integer duration
// arithmetic only and returns nothing when less than MinEtaSpan is left.
func EtaWindows(in EtaInput) []StopEta {
	from := in.Now
	if in.WindowStart.After(from) {
		from = in.WindowStart
	}
	to := in.WindowEnd
	span := to.Sub(from)
	n := len(in.Stops)
	if n == 0 || span < MinEtaSpan {
		return nil
	}
	width := min(EtaWindowWidth, span).Truncate(EtaGrain)
	share := span / time.Duration(2*n)
	out := make([]StopEta, 0, n)
	for i, s := range in.Stops {
		centre := from.Add(share * time.Duration(2*(i+1)-1))
		start := centre.Add(-width / 2)
		if start.Before(from) {
			start = from
		}
		if start.Add(width).After(to) {
			start = to.Add(-width)
		}
		start = start.Truncate(EtaGrain)
		out = append(out, StopEta{
			No: s.No, StopKey: s.StopKey, TrackingNumbers: append([]string(nil), s.TrackingNumbers...),
			Start: start, End: start.Add(width),
		})
	}
	return out
}
