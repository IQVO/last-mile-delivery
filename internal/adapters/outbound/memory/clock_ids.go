package memory

import (
	"fmt"
	"sync"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/partner"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

// Clock is a settable ports.Clock.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock returns a clock reading t.
func NewClock(t time.Time) *Clock { return &Clock{now: t} }

// Now returns the current reading.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Set moves the clock to t.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// Advance moves the clock forward by d.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// SequentialIDs is a ports.IDGenerator minting wave-1, route-1, dsp-1, ... one
// counter per kind.
type SequentialIDs struct {
	mu                      sync.Mutex
	waves, routes, partners int
}

// NewWaveID returns the next wave id.
func (g *SequentialIDs) NewWaveID() wave.ID {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.waves++
	return wave.ID(fmt.Sprintf("wave-%d", g.waves))
}

// NewRouteID returns the next route id.
func (g *SequentialIDs) NewRouteID() route.ID {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.routes++
	return route.ID(fmt.Sprintf("route-%d", g.routes))
}

// NewPartnerID returns the next partner id.
func (g *SequentialIDs) NewPartnerID() partner.ID {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.partners++
	return partner.ID(fmt.Sprintf("dsp-%d", g.partners))
}
