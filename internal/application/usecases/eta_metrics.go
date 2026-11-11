package usecases

import (
	"context"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

// etaSkipWindowElapsed is the reason of a start that raised no ETA because too
// little of the wave's delivery window was left (ADR 0013).
const etaSkipWindowElapsed = "window_elapsed"

// etaMetrics returns the configured EtaMetrics or a no-op.
func (d Deps) etaMetrics() ports.EtaMetrics {
	if d.EtaMetrics != nil {
		return d.EtaMetrics
	}
	return noEtaMetrics{}
}

// noEtaMetrics is the EtaMetrics of a Deps without one.
type noEtaMetrics struct{}

func (noEtaMetrics) EtaAssigned(context.Context, int)   {}
func (noEtaMetrics) EtaSkipped(context.Context, string) {}

// countEtaEvents counts the StopEtaWindowAssigned events among events.
func countEtaEvents(events []shared.Event) int {
	n := 0
	for _, ev := range events {
		if _, ok := ev.(route.StopEtaWindowAssigned); ok {
			n++
		}
	}
	return n
}

// recordEta counts the outcome of one committed StartRoute: the ETA events it
// raised, or one skip when it raised none.
func (uc *StartRoute) recordEta(ctx context.Context, assigned int) {
	if assigned > 0 {
		uc.etaMetrics().EtaAssigned(ctx, assigned)
		return
	}
	uc.etaMetrics().EtaSkipped(ctx, etaSkipWindowElapsed)
}
