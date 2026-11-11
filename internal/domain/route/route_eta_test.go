package route

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

func etaStops(n int) []StopRef {
	out := make([]StopRef, n)
	for i := range out {
		out[i] = StopRef{No: i + 1, StopKey: fmt.Sprintf("k-%d", i+1), TrackingNumbers: []string{fmt.Sprintf("TRK-%d", i+1)}}
	}
	return out
}

func etaInput(now, start time.Time, window time.Duration, n int) EtaInput {
	return EtaInput{Now: now, WindowStart: start, WindowEnd: start.Add(window), Stops: etaStops(n)}
}

// assertEtaProperties checks the properties of ADR 0013 section 2 on any result.
func assertEtaProperties(t *testing.T, in EtaInput, out []StopEta) {
	t.Helper()
	from := maxTime(in.Now, in.WindowStart)
	if len(out) != len(in.Stops) {
		t.Fatalf("%d windows for %d stops", len(out), len(in.Stops))
	}
	prev := time.Time{}
	for i, e := range out {
		assertStopEta(t, in, from, e)
		assertStopIdentity(t, in.Stops[i], e)
		if e.Start.Before(prev) {
			t.Fatalf("stop %d: start %s before the previous stop's %s", e.No, e.Start, prev)
		}
		prev = e.Start
	}
	if again := EtaWindows(in); !reflect.DeepEqual(again, out) {
		t.Fatal("EtaWindows is not deterministic")
	}
}

func assertStopEta(t *testing.T, in EtaInput, from time.Time, e StopEta) {
	t.Helper()
	width := e.End.Sub(e.Start)
	switch {
	case width > EtaWindowWidth || width < MinEtaSpan:
		t.Fatalf("stop %d: width %s out of [%s, %s]", e.No, width, MinEtaSpan, EtaWindowWidth)
	case e.Start.Before(from.Truncate(EtaGrain)) || e.Start.Before(in.WindowStart.Truncate(EtaGrain)):
		t.Fatalf("stop %d: starts %s before %s", e.No, e.Start, from)
	case e.End.After(in.WindowEnd):
		t.Fatalf("stop %d: ends %s after the window end %s", e.No, e.End, in.WindowEnd)
	case e.Start != e.Start.Truncate(EtaGrain) || e.End != e.End.Truncate(EtaGrain):
		t.Fatalf("stop %d: bounds are not on the minute", e.No)
	}
}

func assertStopIdentity(t *testing.T, want StopRef, e StopEta) {
	t.Helper()
	if e.No != want.No || e.StopKey != want.StopKey || !reflect.DeepEqual(e.TrackingNumbers, want.TrackingNumbers) {
		t.Fatalf("stop %d: identity not carried: %+v", e.No, e)
	}
}

func hhmm(h, m int) time.Time { return time.Date(2026, 10, 24, h, m, 0, 0, time.UTC) }

func TestEtaWindowsExactValues(t *testing.T) {
	cases := []struct {
		name   string
		in     EtaInput
		starts []time.Time
		ends   []time.Time
	}{
		{"one stop is centred in an 8h window", etaInput(hhmm(12, 0), hhmm(12, 0), 8*time.Hour, 1), []time.Time{hhmm(15, 0)}, []time.Time{hhmm(17, 0)}},
		{"two stops", etaInput(hhmm(12, 0), hhmm(12, 0), 8*time.Hour, 2), []time.Time{hhmm(13, 0), hhmm(17, 0)}, []time.Time{hhmm(15, 0), hhmm(19, 0)}},
		{"three stops", etaInput(hhmm(12, 0), hhmm(12, 0), 6*time.Hour, 3), []time.Time{hhmm(12, 0), hhmm(14, 0), hhmm(16, 0)}, []time.Time{hhmm(14, 0), hhmm(16, 0), hhmm(18, 0)}},
		{"a window of exactly 2h", etaInput(hhmm(12, 0), hhmm(12, 0), 2*time.Hour, 1), []time.Time{hhmm(12, 0)}, []time.Time{hhmm(14, 0)}},
		{"2h plus one minute truncates to the minute", etaInput(hhmm(12, 0), hhmm(12, 0), 2*time.Hour+time.Minute, 1), []time.Time{hhmm(12, 0)}, []time.Time{hhmm(14, 0)}},
		{"45 minutes: the window is the whole span", etaInput(hhmm(12, 0), hhmm(12, 0), 45*time.Minute, 1), []time.Time{hhmm(12, 0)}, []time.Time{hhmm(12, 45)}},
		{"exactly the minimum span", etaInput(hhmm(12, 0), hhmm(12, 0), MinEtaSpan, 2), []time.Time{hhmm(12, 0), hhmm(12, 0)}, []time.Time{hhmm(12, 30), hhmm(12, 30)}},
		{"now before windowStart uses windowStart", etaInput(hhmm(9, 0), hhmm(12, 0), 8*time.Hour, 1), []time.Time{hhmm(15, 0)}, []time.Time{hhmm(17, 0)}},
		{"now after windowStart narrows the span", etaInput(hhmm(14, 0), hhmm(12, 0), 8*time.Hour, 1), []time.Time{hhmm(16, 0)}, []time.Time{hhmm(18, 0)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := EtaWindows(c.in)
			assertEtaProperties(t, c.in, out)
			for i := range out {
				if !out[i].Start.Equal(c.starts[i]) || !out[i].End.Equal(c.ends[i]) {
					t.Fatalf("stop %d = %s..%s, want %s..%s", i+1, out[i].Start, out[i].End, c.starts[i], c.ends[i])
				}
			}
		})
	}
}

func TestEtaWindowsGivesNothingBelowTheMinimumSpan(t *testing.T) {
	cases := map[string]EtaInput{
		"29 minutes of window":       etaInput(hhmm(12, 0), hhmm(12, 0), 29*time.Minute, 1),
		"now leaves 29 minutes":      etaInput(hhmm(19, 31), hhmm(12, 0), 8*time.Hour, 3),
		"now after the window end":   etaInput(hhmm(21, 0), hhmm(12, 0), 8*time.Hour, 3),
		"no stops":                   etaInput(hhmm(12, 0), hhmm(12, 0), 8*time.Hour, 0),
		"window end before the from": {Now: hhmm(12, 0), WindowStart: hhmm(10, 0), WindowEnd: hhmm(11, 0), Stops: etaStops(1)},
	}
	for name, in := range cases {
		if out := EtaWindows(in); out != nil {
			t.Errorf("%s: windows = %+v, want none", name, out)
		}
	}
}

func TestEtaWindowsPropertiesAcrossShapes(t *testing.T) {
	windows := []time.Duration{8 * time.Hour, 2 * time.Hour, 2*time.Hour + time.Minute, 45 * time.Minute, MinEtaSpan, 5*time.Hour + 17*time.Second}
	nows := []time.Time{hhmm(9, 0), hhmm(12, 0), time.Date(2026, 10, 24, 12, 7, 31, 123, time.UTC), hhmm(13, 45)}
	for _, n := range []int{1, 2, 3, 5, 200} {
		for _, w := range windows {
			for _, now := range nows {
				in := etaInput(now, hhmm(12, 0), w, n)
				if in.WindowEnd.Sub(maxTime(now, in.WindowStart)) < MinEtaSpan {
					continue
				}
				assertEtaProperties(t, in, EtaWindows(in))
			}
		}
	}
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func TestEtaWindowsDoesNotAliasTheInput(t *testing.T) {
	in := etaInput(hhmm(12, 0), hhmm(12, 0), 8*time.Hour, 1)
	out := EtaWindows(in)
	out[0].TrackingNumbers[0] = "changed"
	if in.Stops[0].TrackingNumbers[0] != "TRK-1" {
		t.Fatal("the result shares the input's tracking-number slice")
	}
}
