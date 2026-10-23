// Package system holds the production implementations of the clock and the id
// generator ports.
package system

import (
	"time"

	"github.com/google/uuid"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/domain/partner"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

// Clock reads the wall clock in UTC.
type Clock struct{}

// Now returns the current time in UTC.
func (Clock) Now() time.Time { return time.Now().UTC() }

// IDs mints opaque ids: a readable prefix and a random UUID v4 (ADR 0002 A27).
type IDs struct{}

// NewWaveID returns a new wave id.
func (IDs) NewWaveID() wave.ID { return wave.ID("wave-" + uuid.NewString()) }

// NewRouteID returns a new route id.
func (IDs) NewRouteID() route.ID { return route.ID("route-" + uuid.NewString()) }

// NewPartnerID returns a new partner id.
func (IDs) NewPartnerID() partner.ID { return partner.ID("dsp-" + uuid.NewString()) }

var (
	_ ports.Clock       = Clock{}
	_ ports.IDGenerator = IDs{}
)
