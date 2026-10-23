package system_test

import (
	"strings"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/system"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

func TestClockIsUTCAndCurrent(t *testing.T) {
	before := time.Now()
	got := system.Clock{}.Now()
	if got.Location() != time.UTC || got.Before(before.Add(-time.Second)) || got.After(time.Now().Add(time.Second)) {
		t.Fatalf("Now = %v", got)
	}
}

func TestIDsAreValidUniqueAndPrefixed(t *testing.T) {
	ids := system.IDs{}
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		for prefix, id := range map[string]string{"wave-": string(ids.NewWaveID()), "route-": string(ids.NewRouteID()), "dsp-": string(ids.NewPartnerID())} {
			if !strings.HasPrefix(id, prefix) || !shared.ValidID(id) || seen[id] {
				t.Fatalf("id %q (prefix %s) is invalid or repeated", id, prefix)
			}
			seen[id] = true
		}
	}
}
