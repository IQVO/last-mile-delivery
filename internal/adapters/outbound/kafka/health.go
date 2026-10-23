package kafka

import (
	"context"
	"errors"
	"fmt"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// MetadataClient is the one Kafka call the readiness check makes.
type MetadataClient interface {
	Metadata(ctx context.Context, req *kafkago.MetadataRequest) (*kafkago.MetadataResponse, error)
}

// BrokerCheck answers /readyz's Kafka question: does a broker answer a metadata
// request? The request carries an EMPTY, NON-NIL topic list: an empty list asks
// for no topic (cheap, and it never creates one), whereas a nil list asks for
// every topic of the cluster.
type BrokerCheck struct {
	client MetadataClient
}

// CheckTimeout bounds one metadata request.
const CheckTimeout = 2 * time.Second

// NewBrokerCheck builds a check over the brokers (never dials).
func NewBrokerCheck(brokers []string) *BrokerCheck {
	return NewBrokerCheckWithClient(&kafkago.Client{Addr: kafkago.TCP(brokers...), Timeout: CheckTimeout})
}

// NewBrokerCheckWithClient builds a check over any metadata client.
func NewBrokerCheckWithClient(c MetadataClient) *BrokerCheck { return &BrokerCheck{client: c} }

// Check returns nil when a broker answered the metadata request and listed at
// least one broker.
func (b *BrokerCheck) Check(ctx context.Context) error {
	resp, err := b.client.Metadata(ctx, &kafkago.MetadataRequest{Topics: []string{}})
	if err != nil {
		return fmt.Errorf("kafka metadata: %w", err)
	}
	if resp == nil || len(resp.Brokers) == 0 {
		return errors.New("kafka metadata: the broker answered without listing any broker")
	}
	return nil
}
