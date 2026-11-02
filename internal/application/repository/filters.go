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
	// After is the keyset position of the previous page: only waves strictly
	// after (inductCutoffAt, waveId) are returned. Nil is the first page.
	After *WaveAfter
	// Limit caps the result; zero is no cap.
	Limit int
}

// WaveAfter is the (inductCutoffAt, waveId) position a page continues after.
type WaveAfter struct {
	InductCutoffAt time.Time
	WaveID         string
}
