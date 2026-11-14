package wave

import (
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

const entity = "wave"

// DeliveryWaveScheduled is raised by Schedule. Its fields are the model's data list.
type DeliveryWaveScheduled struct {
	shared.Header
	WaveID         ID
	StationNodeID  string
	ServiceDate    string
	InductCutoffAt time.Time
	WindowStart    time.Time
	WindowEnd      time.Time
	StopCapacity   int
}

// EventName returns "DeliveryWaveScheduled".
func (DeliveryWaveScheduled) EventName() string { return "DeliveryWaveScheduled" }

// Entity returns "wave".
func (DeliveryWaveScheduled) Entity() string { return entity }

// SubjectID returns the waveId.
func (e DeliveryWaveScheduled) SubjectID() string { return string(e.WaveID) }

// DeliveryWaveCapacityChanged is raised by Induct when a new stop is assigned.
type DeliveryWaveCapacityChanged struct {
	shared.Header
	WaveID         ID
	StopCapacity   int
	AssignedStops  int
	RemainingStops int
}

// EventName returns "DeliveryWaveCapacityChanged".
func (DeliveryWaveCapacityChanged) EventName() string { return "DeliveryWaveCapacityChanged" }

// Entity returns "wave".
func (DeliveryWaveCapacityChanged) Entity() string { return entity }

// SubjectID returns the waveId.
func (e DeliveryWaveCapacityChanged) SubjectID() string { return string(e.WaveID) }

// DeliveryWaveCancelled is raised by Cancel (an integration event, G5).
type DeliveryWaveCancelled struct {
	shared.Header
	WaveID ID
	Reason string
}

// EventName returns "DeliveryWaveCancelled".
func (DeliveryWaveCancelled) EventName() string { return "DeliveryWaveCancelled" }

// Entity returns "wave".
func (DeliveryWaveCancelled) Entity() string { return entity }

// SubjectID returns the waveId.
func (e DeliveryWaveCancelled) SubjectID() string { return string(e.WaveID) }

// WaveCostDeclared is raised by DeclareCost: the planner's declared marginal
// cost of ONE stop on the wave, integer cents (ADR 0011, roadmap F7; consumed
// by order-sourcing for its money-denominated AssignmentCost).
type WaveCostDeclared struct {
	shared.Header
	WaveID           ID
	CostPerStopCents int64
	DeclaredAt       time.Time
}

// EventName returns "WaveCostDeclared".
func (WaveCostDeclared) EventName() string { return "WaveCostDeclared" }

// Entity returns "wave".
func (WaveCostDeclared) Entity() string { return entity }

// SubjectID returns the waveId.
func (e WaveCostDeclared) SubjectID() string { return string(e.WaveID) }

// WaveEmissionDeclared is raised by DeclareEmission: the planner's declared
// marginal greenhouse emission of ONE stop on the wave, in grams of CO2
// equivalent (ADR 0016, roadmap C9; consumed by order-sourcing for its
// estimatedCo2eGrams).
type WaveEmissionDeclared struct {
	shared.Header
	WaveID       ID
	GCo2ePerStop int64
	DeclaredAt   time.Time
}

// EventName returns "WaveEmissionDeclared".
func (WaveEmissionDeclared) EventName() string { return "WaveEmissionDeclared" }

// Entity returns "wave".
func (WaveEmissionDeclared) Entity() string { return entity }

// SubjectID returns the waveId.
func (e WaveEmissionDeclared) SubjectID() string { return string(e.WaveID) }

var (
	_ shared.Event = DeliveryWaveScheduled{}
	_ shared.Event = DeliveryWaveCapacityChanged{}
	_ shared.Event = DeliveryWaveCancelled{}
	_ shared.Event = WaveCostDeclared{}
	_ shared.Event = WaveEmissionDeclared{}
)
