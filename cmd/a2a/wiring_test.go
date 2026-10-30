package main

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// A missing DATABASE_URL is a boot error, never a silent in-memory fallback.
func TestBuildDeps_MissingDatabaseFailsBoot(t *testing.T) {
	_, _, err := buildDeps(context.Background(), quietLogger(), "", "")
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("buildDeps without a database = %v, want an error naming DATABASE_URL", err)
	}
}

// An unreachable database is a boot error after the bounded retries.
func TestBuildDeps_BadDatabaseFailsBoot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // a cancelled context makes bootretry give up at once
	if _, _, err := buildDeps(ctx, quietLogger(), "postgres://u@127.0.0.1:1/db", "postgres://u@127.0.0.1:1/db"); err == nil {
		t.Fatal("an unreachable database must fail boot")
	}
}

// buildDeps migrates through MIGRATIONS_DATABASE_URL, not DATABASE_URL: the
// migration URL here is the only unreachable one, so the retried error line
// must name it, and a pool on DATABASE_URL must never have been opened first.
func TestBuildDeps_MigratesThroughTheMigrationsURL(t *testing.T) {
	var logged strings.Builder
	logger := slog.New(slog.NewTextHandler(&logged, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, _, err := buildDeps(ctx, logger, "postgres://app@app-host.invalid:1/db", "postgres://mig@migrations-host.invalid:1/db")
	if err == nil {
		t.Fatal("unreachable databases must fail boot")
	}
	out := logged.String()
	if !strings.Contains(out, "run migrations") || !strings.Contains(out, "migrations-host.invalid") {
		t.Fatalf("the migration step did not use MIGRATIONS_DATABASE_URL; log: %s", out)
	}
	if strings.Contains(out, "app-host.invalid") {
		t.Fatalf("the migration step used DATABASE_URL; log: %s", out)
	}
}

func TestGetenv(t *testing.T) {
	t.Setenv("LMD_A2A_TEST_SET", "v")
	t.Setenv("LMD_A2A_TEST_EMPTY", "")
	if got := getenv("LMD_A2A_TEST_SET", "d"); got != "v" {
		t.Errorf("set: %q", got)
	}
	if got := getenv("LMD_A2A_TEST_EMPTY", "d"); got != "d" {
		t.Errorf("empty: %q", got)
	}
}

func TestNewLoggerLevels(t *testing.T) {
	for level, want := range map[string]slog.Level{"debug": slog.LevelDebug, "WARN": slog.LevelWarn, "warning": slog.LevelWarn, "error": slog.LevelError, "": slog.LevelInfo, "bogus": slog.LevelInfo} {
		if got := newLogger(level); !got.Handler().Enabled(context.Background(), want) || (want > slog.LevelDebug && got.Handler().Enabled(context.Background(), want-1)) {
			t.Errorf("newLogger(%q): level %v not the minimum enabled", level, want)
		}
	}
}
