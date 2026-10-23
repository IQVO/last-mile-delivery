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

var (
	_ shared.Event = DeliveryWaveScheduled{}
	_ shared.Event = DeliveryWaveCapacityChanged{}
	_ shared.Event = DeliveryWaveCancelled{}
)
