package memory

import (
	"context"

	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

// UnitOfWork is the in-memory ports.UnitOfWork: serialised, atomic, re-entrant.
type UnitOfWork struct{ s *Store }

// Do runs fn holding the store. When fn returns an error or panics the state
// taken before fn (aggregates, read models, inbox and outbox) is restored; the
// error is returned unchanged. A Do inside a Do joins it.
func (u *UnitOfWork) Do(ctx context.Context, fn func(ctx context.Context) error) (err error) {
	if ctx.Value(txKey{u.s}) != nil {
		return fn(ctx)
	}
	u.s.mu.Lock()
	defer u.s.mu.Unlock()
	before := u.s.st.clone()
	committed := false
	defer func() {
		if !committed {
			u.s.st = before
		}
	}()
	err = fn(context.WithValue(ctx, txKey{u.s}, struct{}{}))
	committed = err == nil
	return err
}

// Outbox is the in-memory ports.EventOutbox.
type Outbox struct{ s *Store }

// Append stores the events in order.
func (o *Outbox) Append(ctx context.Context, events ...shared.Event) error {
	defer o.s.enter(ctx)()
	o.s.st.outbox = append(o.s.st.outbox, events...)
	return nil
}

// Events returns the appended events in order (a copy of the slice).
func (o *Outbox) Events() []shared.Event {
	o.s.mu.Lock()
	defer o.s.mu.Unlock()
	return append([]shared.Event(nil), o.s.st.outbox...)
}

// Inbox is the in-memory ports.Inbox.
type Inbox struct{ s *Store }

// Claim records the event id and reports whether it was new.
func (i *Inbox) Claim(ctx context.Context, eventID, eventType string) (bool, error) {
	defer i.s.enter(ctx)()
	if _, seen := i.s.st.inbox[eventID]; seen {
		return false, nil
	}
	i.s.st.inbox[eventID] = eventType
	return true, nil
}
