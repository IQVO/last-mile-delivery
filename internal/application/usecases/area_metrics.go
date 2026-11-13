package usecases

import (
	"context"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
)

// areaMetrics returns the configured AreaMetrics or a no-op.
func (d Deps) areaMetrics() ports.AreaMetrics {
	if d.AreaMetrics != nil {
		return d.AreaMetrics
	}
	return noAreaMetrics{}
}

// noAreaMetrics is the AreaMetrics of a Deps without one.
type noAreaMetrics struct{}

func (noAreaMetrics) CarrierAreaSkipped(context.Context) {}
