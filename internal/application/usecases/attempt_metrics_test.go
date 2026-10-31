package usecases_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
)

// recordingMetrics lists the ports.AttemptMetrics calls of a use case.
type recordingMetrics struct {
	mu    sync.Mutex
	calls []string
}

var _ ports.AttemptMetrics = (*recordingMetrics)(nil)

func (m *recordingMetrics) add(call string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, call)
}

func (m *recordingMetrics) AttemptDelivered(context.Context) { m.add("delivered") }
func (m *recordingMetrics) AttemptFailed(context.Context)    { m.add("failed") }
func (m *recordingMetrics) AttemptRejected(_ context.Context, reason string) {
	m.add("rejected/" + reason)
}

func TestRecordAttemptCountsEachReportOnceByOutcome(t *testing.T) {
	e := dispatchedRoute(t, true)
	m := &recordingMetrics{}
	e.deps.Metrics = m
	at := t0.Add(8 * time.Hour)

	_, err := e.attempt("TRK-1", route.OutcomeDelivered, "", at) // route not started
	wantIs(t, err, route.ErrRouteNotStarted)
	e.start(t)
	_, err = e.attempt("TRK-1", route.OutcomeDelivered, "", at)
	noErr(t, err)
	_, err = e.attempt("TRK-2", route.OutcomeFailed, "recipient absent", at)
	noErr(t, err)
	_, err = e.attempt("TRK-1", route.OutcomeDelivered, "", at) // delivered twice
	wantIs(t, err, route.ErrAlreadyDelivered)

	want := []string{"rejected/route-not-started", "delivered", "failed", "rejected/already-delivered"}
	if !reflect.DeepEqual(m.calls, want) {
		t.Fatalf("metric calls = %v, want %v", m.calls, want)
	}
}

func TestRecordAttemptRejectionReasonIsTheProblemSlug(t *testing.T) {
	e := dispatchedRoute(t, false)
	m := &recordingMetrics{}
	e.deps.Metrics = m
	e.start(t)
	_, err := e.attempt("TRK-9", route.OutcomeDelivered, "", t0.Add(8*time.Hour))
	if !errors.Is(err, route.ErrPackageNotOnRoute) {
		t.Fatalf("Execute = %v, want package-not-on-route", err)
	}
	if !reflect.DeepEqual(m.calls, []string{"rejected/package-not-on-route"}) {
		t.Fatalf("metric calls = %v", m.calls)
	}
}
