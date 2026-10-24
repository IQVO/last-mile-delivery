package kafka

import (
	"errors"
	"fmt"
	"io"
	"testing"

	kafkago "github.com/segmentio/kafka-go"
)

// isTopicNotReady decides whether writeDLQ retries: only the transient
// not-ready states of a just auto-created DLQ topic may be retried, every
// other failure must surface at once.
func TestIsTopicNotReady(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"no error", nil, false},
		{"unknown topic or partition", kafkago.UnknownTopicOrPartition, true},
		{"leader not available", kafkago.LeaderNotAvailable, true},
		{"a wrapped not-ready state", fmt.Errorf("write to dlq: %w", kafkago.LeaderNotAvailable), true},
		{"an unrelated error", io.ErrUnexpectedEOF, false},
		{"a plain sentinel error", errors.New("kafka: broker down"), false},
		{"no write errors at all", kafkago.WriteErrors{}, false},
		{"write errors of only not-ready states", kafkago.WriteErrors{nil, kafkago.LeaderNotAvailable}, true},
		{"write errors with a hard failure", kafkago.WriteErrors{kafkago.UnknownTopicOrPartition, io.ErrUnexpectedEOF}, false},
		{"write errors of only nil entries", kafkago.WriteErrors{nil, nil}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTopicNotReady(tc.err); got != tc.want {
				t.Fatalf("isTopicNotReady(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
