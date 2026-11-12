package deliverypoint

import (
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

const entity = "deliverypointprofile"

// DeliveryPointFlagged is raised by Observe while a point has at least
// FlagAfterStrikes open strikes and an observation reached packages that were
// not told yet. TrackingNumbers holds only those packages (1 to MaxStrikes);
// Failures is the number of strikes open at that moment; FlaggedAt is the attempt
// instant that triggered it. No event means "no new package was affected".
type DeliveryPointFlagged struct {
	shared.Header
	StopKey         string
	Reason          Category
	Failures        int
	TrackingNumbers []string
	FlaggedAt       time.Time
}

// EventName returns "DeliveryPointFlagged".
func (DeliveryPointFlagged) EventName() string { return "DeliveryPointFlagged" }

// Entity returns "deliverypointprofile".
func (DeliveryPointFlagged) Entity() string { return entity }

// SubjectID returns the stopKey.
func (e DeliveryPointFlagged) SubjectID() string { return e.StopKey }

var _ shared.Event = DeliveryPointFlagged{}
