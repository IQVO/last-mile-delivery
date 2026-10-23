package kafka

import (
	"context"
	"errors"
	"strings"
	"testing"

	kafkago "github.com/segmentio/kafka-go"
)

type fakeLister struct {
	brokers []kafkago.Broker
	err     error
}

func (f fakeLister) Brokers(context.Context) ([]kafkago.Broker, error) { return f.brokers, f.err }

func TestBrokerCheckHealthyWhenABrokerAnswers(t *testing.T) {
	c := NewBrokerCheckWithLister(fakeLister{brokers: []kafkago.Broker{{ID: 1}}})
	if err := c.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestBrokerCheckFailsWhenTheLookupFails(t *testing.T) {
	boom := errors.New("connection refused")
	err := NewBrokerCheckWithLister(fakeLister{err: boom}).Check(context.Background())
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap %v", err, boom)
	}
}

func TestBrokerCheckFailsWhenTheBrokerListsNoBroker(t *testing.T) {
	err := NewBrokerCheckWithLister(fakeLister{}).Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "without listing any broker") {
		t.Fatalf("err = %v", err)
	}
}

func TestDialListerWithoutAddressFails(t *testing.T) {
	err := NewBrokerCheck([]string{" ", ""}).Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no broker address configured") {
		t.Fatalf("err = %v, want the missing address named", err)
	}
}

func TestDialListerReportsEveryUnreachableAddress(t *testing.T) {
	// Port 1 and 2 on loopback refuse at once: no broker, no wait.
	err := NewBrokerCheck([]string{"127.0.0.1:1", "127.0.0.1:2"}).Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "127.0.0.1:1") || !strings.Contains(err.Error(), "127.0.0.1:2") {
		t.Fatalf("err = %v, want both addresses named", err)
	}
}
