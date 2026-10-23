package history

import "github.com/claudioed/last-mile-delivery/internal/domain/shared"

const entity = "packageattempthistory"

// PackageInductionDeferred is raised when a package at the station finds no
// wave (Gap Register G16). AssignmentID is empty when no label data was seen.
type PackageInductionDeferred struct {
	shared.Header
	TrackingNumber string
	AssignmentID   string
}

// EventName returns "PackageInductionDeferred".
func (PackageInductionDeferred) EventName() string { return "PackageInductionDeferred" }

// Entity returns "packageattempthistory".
func (PackageInductionDeferred) Entity() string { return entity }

// SubjectID returns the trackingNumber.
func (e PackageInductionDeferred) SubjectID() string { return e.TrackingNumber }

// PackageReturnInitiated is raised by the third failed attempt of a package.
type PackageReturnInitiated struct {
	shared.Header
	TrackingNumber string
}

// EventName returns "PackageReturnInitiated".
func (PackageReturnInitiated) EventName() string { return "PackageReturnInitiated" }

// Entity returns "packageattempthistory".
func (PackageReturnInitiated) Entity() string { return entity }

// SubjectID returns the trackingNumber.
func (e PackageReturnInitiated) SubjectID() string { return e.TrackingNumber }

var (
	_ shared.Event = PackageInductionDeferred{}
	_ shared.Event = PackageReturnInitiated{}
)
