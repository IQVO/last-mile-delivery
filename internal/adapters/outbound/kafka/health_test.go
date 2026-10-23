package kafka

import (
	"context"
	"errors"
	"testing"

	kafkago "github.com/segmentio/kafka-go"
)

type fakeMetadata struct {
	resp *kafkago.MetadataResponse
	err  error
	got  *kafkago.MetadataRequest
}

func (f *fakeMetadata) Metadata(_ context.Context, req *kafkago.MetadataRequest) (*kafkago.MetadataResponse, error) {
	f.got = req
	return f.resp, f.err
}

func TestBrokerCheckAsksForNoTopicWithAnEmptyNonNilList(t *testing.T) {
	f := &fakeMetadata{resp: &kafkago.MetadataResponse{Brokers: []kafkago.Broker{{ID: 1}}}}
	if err := NewBrokerCheckWithClient(f).Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if f.got == nil || f.got.Topics == nil || len(f.got.Topics) != 0 {
		t.Fatalf("topics = %#v, want an empty non-nil list (nil asks for every topic)", f.got)
	}
}

func TestBrokerCheckFailures(t *testing.T) {
	boom := errors.New("boom")
	for name, f := range map[string]*fakeMetadata{
		"transport error":    {err: boom},
		"no broker listed":   {resp: &kafkago.MetadataResponse{}},
		"no response at all": {},
	} {
		err := NewBrokerCheckWithClient(f).Check(context.Background())
		if err == nil {
			t.Errorf("%s: want an error", name)
		}
		if name == "transport error" && !errors.Is(err, boom) {
			t.Errorf("%s: the cause must be wrapped: %v", name, err)
		}
	}
}

func TestNewBrokerCheckNeverDials(t *testing.T) {
	if NewBrokerCheck([]string{"127.0.0.1:1"}) == nil {
		t.Fatal("constructor returned nil")
	}
}
