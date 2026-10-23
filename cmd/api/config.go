package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	outboxrelay "github.com/claudioed/last-mile-delivery/internal/adapters/outbound/outbox"
)

var (
	errMissingDatabaseURL = errors.New("DATABASE_URL is required: this context runs on real Postgres, there is no in-memory mode")
	errMissingBrokers     = errors.New("KAFKA_BROKERS is required: this context runs on real Kafka (outbox relay, consumers, /readyz)")
)

// The consumer group of each consumer is a mandatory environment variable: a
// shared or defaulted group would let two services steal each other's offsets.
const (
	linehaulGroupEnv = "LINEHAUL_CONSUMER_GROUP"
	topologyGroupEnv = "NODE_TOPOLOGY_CONSUMER_GROUP"
	shippingGroupEnv = "SHIPPING_CONSUMER_GROUP"
)

// runtimeEnv is the validated environment the service boots with.
type runtimeEnv struct {
	databaseURL string
	brokers     []string
	groups      map[string]string
}

// loadRuntime validates the environment before anything is dialled.
func loadRuntime(env func(string) string) (runtimeEnv, error) {
	if env("DATABASE_URL") == "" {
		return runtimeEnv{}, errMissingDatabaseURL
	}
	rt := runtimeEnv{databaseURL: env("DATABASE_URL"), brokers: splitBrokers(env("KAFKA_BROKERS")), groups: map[string]string{}}
	if len(rt.brokers) == 0 {
		return runtimeEnv{}, errMissingBrokers
	}
	for _, name := range []string{linehaulGroupEnv, topologyGroupEnv, shippingGroupEnv} {
		group := strings.TrimSpace(env(name))
		if group == "" {
			return runtimeEnv{}, fmt.Errorf("%s is required: the consumer always runs on real Kafka", name)
		}
		rt.groups[name] = group
	}
	return rt, nil
}

// splitBrokers splits KAFKA_BROKERS on commas, trimming blanks.
func splitBrokers(raw string) []string {
	var out []string
	for _, b := range strings.Split(raw, ",") {
		if b = strings.TrimSpace(b); b != "" {
			out = append(out, b)
		}
	}
	return out
}

// parseRelayInterval reads OUTBOX_RELAY_INTERVAL as a duration or a number of
// seconds; anything else logs a warning and uses the default.
func parseRelayInterval(raw string, logger *slog.Logger) time.Duration {
	if raw == "" {
		return outboxrelay.DefaultInterval
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		if secs, serr := strconv.ParseFloat(raw, 64); serr == nil {
			d, err = time.Duration(secs*float64(time.Second)), nil
		}
	}
	if err != nil || d <= 0 {
		logger.Warn("invalid OUTBOX_RELAY_INTERVAL; using the default", "value", raw, "default", outboxrelay.DefaultInterval)
		return outboxrelay.DefaultInterval
	}
	return d
}

// outboxMaxAttempts reads OUTBOX_MAX_ATTEMPTS: how many times an outbox row may
// fail before the relay stops claiming it. Unset is the default; 0 is
// unlimited; a negative or unparsable value logs a warning and uses the
// default, so a typo never silently removes the cap.
func outboxMaxAttempts(raw string, logger *slog.Logger) int {
	if raw == "" {
		return outboxrelay.DefaultMaxAttempts
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		logger.Warn("invalid OUTBOX_MAX_ATTEMPTS; using the default", "value", raw, "default", outboxrelay.DefaultMaxAttempts)
		return outboxrelay.DefaultMaxAttempts
	}
	return n
}

// shutdownDrainDelay reads SHUTDOWN_DRAIN_DELAY: how long /readyz reports
// not-ready before the HTTP server stops accepting.
func shutdownDrainDelay(logger *slog.Logger) time.Duration {
	raw := os.Getenv("SHUTDOWN_DRAIN_DELAY")
	if raw == "" {
		return defaultShutdownDrainDelay
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		logger.Warn("ignoring invalid SHUTDOWN_DRAIN_DELAY", "value", raw, "default", defaultShutdownDrainDelay.String())
		return defaultShutdownDrainDelay
	}
	return d
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
