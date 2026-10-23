package kafka

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// BrokerLister asks ONE live broker for the cluster's broker list: a real
// metadata round trip, never an answer from a cache.
type BrokerLister interface {
	Brokers(ctx context.Context) ([]kafkago.Broker, error)
}

// BrokerCheck answers /readyz's Kafka question: does a broker answer a metadata
// request? The request carries an EMPTY, NON-NIL topic list (kafka-go's
// Conn.Brokers sends zero topics, not the null list that means "all topics"):
// the answer is small and asking never creates a topic. It does not look at
// topics at all: the output topic does not exist until the first publish, and a
// not-ready pod receives no traffic and could never be driven to publish.
//
// It dials a FRESH connection on every call on purpose: kafka-go's
// Client.Metadata is served from the transport's cached cluster layout once a
// metadata response was seen, so a check built on it keeps answering ready
// after the broker is gone.
type BrokerCheck struct {
	lister BrokerLister
}

// dialTimeout bounds one dial of the check; the readiness gate bounds the whole
// round on top of it.
const dialTimeout = 2 * time.Second

// NewBrokerCheck builds a check over the broker addresses (never dials).
func NewBrokerCheck(brokers []string) *BrokerCheck {
	return NewBrokerCheckWithLister(dialLister{brokers: brokers, dialer: &kafkago.Dialer{Timeout: dialTimeout}})
}

// NewBrokerCheckWithLister builds a check over any lister (a fake in tests).
func NewBrokerCheckWithLister(l BrokerLister) *BrokerCheck { return &BrokerCheck{lister: l} }

// Check returns nil when a broker answered a topic-less metadata request;
// otherwise an error wrapping the cause. ctx bounds the round trip.
func (b *BrokerCheck) Check(ctx context.Context) error {
	brokers, err := b.lister.Brokers(ctx)
	if err != nil {
		return fmt.Errorf("kafka metadata: %w", err)
	}
	if len(brokers) == 0 {
		return errors.New("kafka metadata: the broker answered without listing any broker")
	}
	return nil
}

// dialLister dials the configured addresses in order and asks the first one
// that answers.
type dialLister struct {
	brokers []string
	dialer  *kafkago.Dialer
}

func (d dialLister) Brokers(ctx context.Context) ([]kafkago.Broker, error) {
	var errs []string
	for _, addr := range d.brokers {
		if strings.TrimSpace(addr) == "" {
			continue
		}
		brokers, err := d.ask(ctx, strings.TrimSpace(addr))
		if err == nil {
			return brokers, nil
		}
		errs = append(errs, err.Error())
	}
	if len(errs) == 0 {
		return nil, errors.New("no broker address configured")
	}
	return nil, errors.New(strings.Join(errs, "; "))
}

// ask dials addr, sends one empty-topic metadata request and closes.
func (d dialLister) ask(ctx context.Context, addr string) ([]kafkago.Broker, error) {
	conn, err := d.dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return nil, fmt.Errorf("deadline %s: %w", addr, err)
		}
	}
	brokers, err := conn.Brokers()
	if err != nil {
		return nil, fmt.Errorf("ask %s: %w", addr, err)
	}
	return brokers, nil
}
