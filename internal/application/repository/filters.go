// Package repository holds the non-interface vocabulary of the repository
// ports: the list filters. It has no behaviour; the interfaces are in
// internal/application/ports.
package repository

import (
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

// WaveFilter narrows WaveRepository.List. A zero field does not filter; the set
// fields combine with AND. The result is ordered by inductCutoffAt then waveId.
type WaveFilter struct {
	StationNodeID string
	ServiceDate   string
	State         wave.State
	// InductCutoffAfter keeps the waves whose cutoff is strictly after it.
	InductCutoffAfter time.Time
}
