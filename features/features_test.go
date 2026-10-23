// Package features_test is the godog (Gherkin) acceptance suite of the use
// cases, run on the in-memory test doubles. The runner lives here, not at the
// repository root (TestNoGoFilesAtRepoRoot).
package features_test

import (
	"testing"

	"github.com/cucumber/godog"
)

func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: initializeScenario,
		Options:             &godog.Options{Format: "pretty", Paths: []string{"."}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("the godog scenarios failed")
	}
}
