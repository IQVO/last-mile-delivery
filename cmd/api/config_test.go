package main

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	outboxrelay "github.com/claudioed/last-mile-delivery/internal/adapters/outbound/outbox"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func fullEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL": "postgres://x", "KAFKA_BROKERS": "b1:9092, b2:9092,",
		linehaulGroupEnv: "g-line", topologyGroupEnv: "g-topo", shippingGroupEnv: "g-ship",
	}
}

func TestLoadRuntimeReadsAFullEnvironment(t *testing.T) {
	rt, err := loadRuntime(envOf(fullEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if len(rt.brokers) != 2 || rt.brokers[1] != "b2:9092" || rt.databaseURL != "postgres://x" || rt.groups[topologyGroupEnv] != "g-topo" {
		t.Fatalf("runtime = %+v", rt)
	}
}

func TestLoadRuntimeNamesWhatIsMissing(t *testing.T) {
	cases := []struct {
		drop string
		want string
	}{
		{"DATABASE_URL", "DATABASE_URL is required"},
		{"KAFKA_BROKERS", "KAFKA_BROKERS is required"},
		{linehaulGroupEnv, linehaulGroupEnv + " is required"},
		{topologyGroupEnv, topologyGroupEnv + " is required"},
		{shippingGroupEnv, shippingGroupEnv + " is required"},
	}
	for _, c := range cases {
		env := fullEnv()
		env[c.drop] = "  "
		if c.drop == "DATABASE_URL" || c.drop == "KAFKA_BROKERS" {
			delete(env, c.drop)
		}
		_, err := loadRuntime(envOf(env))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("without %s: %v, want %q", c.drop, err, c.want)
		}
	}
	env := fullEnv()
	env["KAFKA_BROKERS"] = " , "
	if _, err := loadRuntime(envOf(env)); !errors.Is(err, errMissingBrokers) {
		t.Errorf("blank brokers: %v", err)
	}
}

func TestSplitBrokers(t *testing.T) {
	if got := splitBrokers(""); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	if got := splitBrokers("a:1,,b:2"); len(got) != 2 || got[0] != "a:1" {
		t.Fatalf("%v", got)
	}
}

func warnLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

func TestOutboxMaxAttempts(t *testing.T) {
	tests := []struct {
		raw  string
		want int
		warn bool
	}{{"", 20, false}, {"5", 5, false}, {" 7 ", 7, false}, {"0", 0, false}, {"-1", 20, true}, {"many", 20, true}}
	for _, tt := range tests {
		logger, buf := warnLogger()
		if got := outboxMaxAttempts(tt.raw, logger); got != tt.want {
			t.Errorf("outboxMaxAttempts(%q) = %d, want %d", tt.raw, got, tt.want)
		}
		if warned := strings.Contains(buf.String(), "level=WARN"); warned != tt.warn {
			t.Errorf("outboxMaxAttempts(%q) warned = %v", tt.raw, warned)
		}
	}
	if outboxrelay.DefaultMaxAttempts != 20 {
		t.Fatalf("DefaultMaxAttempts = %d", outboxrelay.DefaultMaxAttempts)
	}
}

func TestParseRelayInterval(t *testing.T) {
	def := outboxrelay.DefaultInterval
	tests := []struct {
		raw  string
		want time.Duration
		warn bool
	}{{"", def, false}, {"250ms", 250 * time.Millisecond, false}, {"2", 2 * time.Second, false}, {"0.5", 500 * time.Millisecond, false}, {"0", def, true}, {"-3s", def, true}, {"soon", def, true}}
	for _, tt := range tests {
		logger, buf := warnLogger()
		if got := parseRelayInterval(tt.raw, logger); got != tt.want {
			t.Errorf("parseRelayInterval(%q) = %v, want %v", tt.raw, got, tt.want)
		}
		if warned := strings.Contains(buf.String(), "level=WARN"); warned != tt.warn {
			t.Errorf("parseRelayInterval(%q) warned = %v", tt.raw, warned)
		}
	}
}

func TestShutdownDrainDelay(t *testing.T) {
	logger, _ := warnLogger()
	for raw, want := range map[string]time.Duration{"": defaultShutdownDrainDelay, "0s": 0, "1500ms": 1500 * time.Millisecond, "-1s": defaultShutdownDrainDelay, "x": defaultShutdownDrainDelay} {
		t.Setenv("SHUTDOWN_DRAIN_DELAY", raw)
		if got := shutdownDrainDelay(logger); got != want {
			t.Errorf("shutdownDrainDelay(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestLogLevelAndGetenv(t *testing.T) {
	for level, want := range map[string]slog.Level{"debug": slog.LevelDebug, "WARN": slog.LevelWarn, "warning": slog.LevelWarn, "error": slog.LevelError, "info": slog.LevelInfo, "": slog.LevelInfo, "loud": slog.LevelInfo} {
		if got := newLogger(level).Handler().Enabled(nil, want); !got { //nolint:staticcheck // a nil context is accepted by the slog handlers
			t.Errorf("level %q does not enable %v", level, want)
		}
	}
	t.Setenv("LMD_TEST_VAR", "")
	if getenv("LMD_TEST_VAR", "fallback") != "fallback" {
		t.Fatal("empty means fallback")
	}
	t.Setenv("LMD_TEST_VAR", "set")
	if getenv("LMD_TEST_VAR", "fallback") != "set" {
		t.Fatal("set wins")
	}
}
